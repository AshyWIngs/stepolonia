package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapRejectsSecretArguments(t *testing.T) {
	for _, args := range [][]string{{commandBootstrapAdmin, "password"}, {commandBootstrapAdmin, "--password=secret"}} {
		if validateCommand(args) == nil {
			t.Fatal("bootstrap accepted secret-bearing arguments")
		}
	}
	if err := validateCommand([]string{commandBootstrapAdmin}); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapRejectsRedirection(t *testing.T) {
	if err := runBootstrap(context.Background()); err == nil {
		t.Fatal("nonterminal bootstrap must fail closed")
	}
}

func TestBootstrapKeyFileBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(totpKeyFileEnv, path)
	t.Setenv("ADMIN_TOTP_KEY_VERSION", "1")
	key, version, err := bootstrapKey()
	if err != nil || len(key) != 32 || version != 1 {
		t.Fatalf("private key failed: %v", err)
	}
	for _, mode := range []os.FileMode{0640, 0604, 0644} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, _, err := bootstrapKey(); err == nil {
			t.Fatalf("accepted shared key %o", mode)
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(totpKeyFileEnv, link)
	if _, _, err := bootstrapKey(); err == nil {
		t.Fatal("accepted symlink key")
	}
	t.Setenv(totpKeyFileEnv, dir)
	if _, _, err := bootstrapKey(); err == nil {
		t.Fatal("accepted directory key")
	}
	t.Setenv(totpKeyFileEnv, path)
	if err := os.WriteFile(path, make([]byte, 33), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bootstrapKey(); err == nil {
		t.Fatal("accepted oversized key")
	}
	t.Setenv("ADMIN_TOTP_KEY_VERSION", "0")
	if _, _, err := bootstrapKey(); err == nil {
		t.Fatal("accepted zero version")
	}
}
