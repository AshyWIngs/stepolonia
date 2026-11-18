package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/config"
	"stepolonia/apps/api/internal/database"
)

const totpKeyFileEnv = "ADMIN_TOTP_KEY_FILE"

// Enrollment secrets are written only to the controlling terminal, never to a
// pipe or a log. Password input bypasses the line reader and disables echo.
func runBootstrap(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) {
		return inputError{"bootstrap-admin requires a local interactive terminal without redirection"}
	}
	site := os.Getenv("SITE")
	key, version, err := bootstrapKey()
	if err != nil {
		return err
	}
	defer clear(key)
	cfg, err := toolConfig()
	if err != nil {
		return err
	}
	db := database.Open(cfg)
	defer func() { _ = db.Close() }()
	if err := database.CheckSchema(ctx, db); err != nil {
		return err
	}
	service, err := identity.NewBootstrapService(db, identity.BootstrapConfig{Site: site, KeyVersion: version, EncryptionKey: key})
	if err != nil {
		return err
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return inputError{"cannot open controlling terminal"}
	}
	defer func() { _ = tty.Close() }()
	state, err := term.GetState(int(tty.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(tty.Fd()), state) }()
	ui := bootstrapTerminal{file: tty, reader: bufio.NewReaderSize(&contextTerminal{ctx: ctx, file: tty}, 1024)}
	if _, err := fmt.Fprintf(tty, "Bootstrap SITE=%q DB=%q USER=%q\n", site, cfg.Database, cfg.User); err != nil {
		return err
	}
	confirm, err := ui.line(ctx, "Type SITE/DB to confirm the selected installation: ")
	if err != nil {
		return err
	}
	if confirm != site+"/"+cfg.Database {
		return inputError{"installation confirmation did not match"}
	}
	return ui.enroll(ctx, service)
}

func (t *bootstrapTerminal) enroll(ctx context.Context, service *identity.BootstrapService) error {
	login, err := t.line(ctx, "Login identifier: ")
	if err != nil {
		return err
	}
	password, err := t.confirmPassword(ctx)
	if err != nil {
		return err
	}
	defer clear(password)
	enrollment, err := service.Start(ctx, login, string(password))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(t.file, "Save this TOTP enrollment in your authenticator. It will not be shown again.\n%s\nSecret: %s\n", enrollment.ProvisioningURI, enrollment.Secret); err != nil {
		return err
	}
	code, err := t.password(ctx, "Current six-digit TOTP: ")
	if err != nil {
		return err
	}
	defer clear(code)
	codes, err := service.Complete(ctx, login, string(password), string(code))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(t.file, "Administrator activated. Store these recovery codes securely; each is shown only once:"); err != nil {
		return err
	}
	for _, code := range codes {
		if _, err := fmt.Fprintln(t.file, code); err != nil {
			return err
		}
	}
	return nil
}

func (t *bootstrapTerminal) confirmPassword(ctx context.Context) ([]byte, error) {
	password, err := t.password(ctx, "Password (15–128 Unicode characters): ")
	if err != nil {
		return nil, err
	}
	again, err := t.password(ctx, "Repeat password: ")
	if err != nil {
		clear(password)
		return nil, err
	}
	same := subtle.ConstantTimeCompare(password, again) == 1
	clear(again)
	if !same {
		clear(password)
		return nil, inputError{"passwords do not match"}
	}
	return password, nil
}

type bootstrapTerminal struct {
	file   *os.File
	reader *bufio.Reader
}

// Select keeps both canonical line input and raw password input cancellable.
// Darwin cannot poll /dev/tty; select supports the controlling terminal.
// No goroutine retains a terminal read or prevents close after an interrupt.
type contextTerminal struct {
	ctx  context.Context
	file *os.File
}

func (t *contextTerminal) Read(data []byte) (int, error) {
	fd := int(t.file.Fd())
	if fd < 0 || fd >= unix.FD_SETSIZE {
		return 0, fmt.Errorf("invalid terminal descriptor")
	}
	for {
		if err := t.waitReady(fd); err != nil {
			return 0, err
		}
		n, err := unix.Read(fd, data)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return n, err
	}
}
func (t *contextTerminal) waitReady(fd int) error {
	for {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		var ready unix.FdSet
		ready.Set(fd)
		timeout := unix.Timeval{Usec: 100000}
		n, err := unix.Select(fd+1, &ready, nil, nil, &timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		return nil
	}
}
func (t *contextTerminal) Write(data []byte) (int, error) { return t.file.Write(data) }

func (t *bootstrapTerminal) line(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(t.file, prompt); err != nil {
		return "", err
	}
	data, err := t.reader.ReadSlice('\n')
	if err != nil {
		return "", inputError{"terminal input ended or exceeded the input limit"}
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
}

func (t *bootstrapTerminal) password(ctx context.Context, prompt string) ([]byte, error) {
	state, err := term.MakeRaw(int(t.file.Fd()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = term.Restore(int(t.file.Fd()), state) }()
	terminal := term.NewTerminal(&contextTerminal{ctx: ctx, file: t.file}, "")
	password, err := terminal.ReadPassword(prompt)
	return []byte(password), err
}

// The key is provisioned by the operator outside PostgreSQL. Reject symlinks,
// group-readable files and wrong ownership before reading any key bytes.
func bootstrapKey() ([]byte, int, error) {
	key, version, err := config.InstallationKey()
	if err != nil {
		return nil, 0, inputError{err.Error()}
	}
	return key, version, nil
}
