package cms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// BootstrapConfig binds operator enrollment to an explicit installation and external key.
type BootstrapConfig struct {
	Site          string
	KeyVersion    int
	EncryptionKey []byte
}

// BootstrapService is an operator-only enrollment path; it never issues sessions.
type BootstrapService struct {
	db     *sql.DB
	config BootstrapConfig
}

// BootstrapEnrollment contains a newly issued seed, returned only after pending commit.
type BootstrapEnrollment struct {
	UserID          string
	Secret          string
	ProvisioningURI string
}

var errBootstrapDenied = errors.New("bootstrap credential rejected")

var bootstrapSitePattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
var bootstrapHashSlots = make(chan struct{}, 4)

func bootstrapHashSlot(ctx context.Context) (func(), error) {
	select {
	case bootstrapHashSlots <- struct{}{}:
		return func() { <-bootstrapHashSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, fmt.Errorf("bootstrap password capacity exceeded")
	}
}

// NewBootstrapService requires a 256-bit external installation key.
func NewBootstrapService(db *sql.DB, config BootstrapConfig) (*BootstrapService, error) {
	if db == nil || !bootstrapSitePattern.MatchString(config.Site) || len(config.Site) > 253 || config.KeyVersion < 1 || len(config.EncryptionKey) != 32 {
		return nil, fmt.Errorf("bootstrap requires explicit SITE, positive key version and 32-byte encryption key")
	}
	config.EncryptionKey = append([]byte(nil), config.EncryptionKey...)
	return &BootstrapService{db: db, config: config}, nil
}
func normalizeLogin(login string) (string, error) {
	if !utf8.ValidString(login) {
		return "", fmt.Errorf("invalid login")
	}
	key := cases.Fold().String(norm.NFC.String(login))
	if key == "" || len(key) > 256 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\x00\r\n") {
		return "", fmt.Errorf("invalid login")
	}
	return key, nil
}
func (s *BootstrapService) operator(ctx context.Context) error {
	var owner bool
	if err := s.db.QueryRowContext(ctx, `SELECT current_user = pg_catalog.pg_get_userbyid(datdba) AND session_user = current_user FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&owner); err != nil {
		return fmt.Errorf("verify bootstrap operator: %w", err)
	}
	if !owner {
		return fmt.Errorf("bootstrap requires direct database owner credential")
	}
	return nil
}

type bootstrapSnapshot struct {
	id, hash string
	version  int64
}

func bootstrapVerify(ctx context.Context, password, hash string) (bool, error) {
	release, err := bootstrapHashSlot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	ok := verifyPassword(password, hash)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return ok, nil
}
func bootstrapHash(ctx context.Context, password string) (string, error) {
	release, err := bootstrapHashSlot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hash, nil
}
func (s *BootstrapService) snapshot(ctx context.Context, login string) (bootstrapSnapshot, error) {
	var x bootstrapSnapshot
	err := s.db.QueryRowContext(ctx, `SELECT id::text, COALESCE(password_hash,''),credential_version FROM public.users WHERE login_key=$1`, login).Scan(&x.id, &x.hash, &x.version)
	return x, err
}
func (s *BootstrapService) state(ctx context.Context, tx *sql.Tx) (string, error) {
	var owner bool
	if err := tx.QueryRowContext(ctx, `SELECT current_user = pg_catalog.pg_get_userbyid(datdba) AND session_user = current_user FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&owner); err != nil {
		return "", err
	}
	if !owner {
		return "", fmt.Errorf("bootstrap transaction requires direct database owner credential")
	}

	var site sql.NullString
	var completed bool
	var id sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT site,bootstrap_completed,bootstrap_user_id::text FROM public.security_state WHERE id=1`).Scan(&site, &completed, &id); err != nil {
		return "", fmt.Errorf("read bootstrap state: %w", err)
	}
	if completed {
		return "", fmt.Errorf("bootstrap is permanently completed")
	}
	if site.Valid && site.String != s.config.Site {
		return "", fmt.Errorf("SITE does not match installation")
	}
	return id.String, nil
}
func bootstrapAudit(ctx context.Context, tx *sql.Tx, operation, user string) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.security_audit(id,operation,actor_id,target_id,reason) VALUES($1,$2,$3,$3,'')`, id, operation, user)
	return err
}

// Start creates pending enrollment or replaces an interrupted seed after password verification.
// Previously issued seeds are never returned again. No pending identity can authenticate.
func (s *BootstrapService) Start(ctx context.Context, login, password string) (BootstrapEnrollment, error) {
	var out BootstrapEnrollment
	login, err := normalizeLogin(login)
	if err != nil {
		return out, err
	}
	if err = s.operator(ctx); err != nil {
		return out, err
	}
	snap, exists, err := s.prepareStart(ctx, login, password)
	if err != nil {
		return out, s.auditBootstrapDenial(ctx, snap, err)
	}
	secret, err := newSecret()
	if err != nil {
		return out, err
	}
	ciphertext, err := encryptSeed(s.config.EncryptionKey, s.config.Site, snap.id, s.config.KeyVersion, secret)
	if err != nil {
		return out, err
	}
	err = WithWriteMutex(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
		return s.startTx(ctx, tx, login, snap.hash, snap, exists, ciphertext)
	})
	if err != nil {
		return out, fmt.Errorf("start bootstrap: %w", s.auditBootstrapDenial(ctx, snap, err))
	}
	issuer := "Stepolonia " + s.config.Site
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + issuer + ":" + login}
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	u.RawQuery = q.Encode()
	return BootstrapEnrollment{UserID: snap.id, Secret: secret, ProvisioningURI: u.String()}, nil
}

// Complete confirms the new TOTP and atomically activates the sole initial admin.
// Codes are returned once, after commit; interruption never rolls back a committed marker.
func (s *BootstrapService) Complete(ctx context.Context, login, password, code string) ([]string, error) {
	login, err := normalizeLogin(login)
	if err != nil {
		return nil, err
	}
	if err = s.operator(ctx); err != nil {
		return nil, err
	}
	snap, err := s.snapshot(ctx, login)
	if err != nil {
		return nil, errBootstrapDenied
	}
	valid, err := bootstrapVerify(ctx, password, snap.hash)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, s.auditBootstrapDenial(ctx, snap, errBootstrapDenied)
	}
	codes, digests, err := recoveryCodes()
	if err != nil {
		return nil, err
	}
	err = WithWriteMutex(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
		return s.completeTx(ctx, tx, snap, code, digests)
	})
	if err != nil {
		return nil, fmt.Errorf("complete bootstrap: %w", s.auditBootstrapDenial(ctx, snap, err))
	}
	return codes, nil
}

func (s *BootstrapService) startTx(ctx context.Context, tx *sql.Tx, login, hash string, snap bootstrapSnapshot, exists bool, ciphertext []byte) error {
	pending, err := s.state(ctx, tx)
	if err != nil {
		return err
	}
	if exists {
		return s.restartTx(ctx, tx, pending, snap, ciphertext)
	}
	if pending != "" {
		return fmt.Errorf("unfinished bootstrap exists; same operator login and password required")
	}
	var empty bool
	if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM public.users) AND NOT EXISTS(SELECT 1 FROM public.roles) AND NOT EXISTS(SELECT 1 FROM public.user_roles) AND NOT EXISTS(SELECT 1 FROM public.role_grants) AND NOT EXISTS(SELECT 1 FROM public.sessions) AND NOT EXISTS(SELECT 1 FROM public.mfa_totp) AND NOT EXISTS(SELECT 1 FROM public.recovery_codes) AND NOT EXISTS(SELECT 1 FROM public.security_audit)`).Scan(&empty); err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("bootstrap requires empty security state")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.users(id,login_key,password_hash) VALUES($1,$2,$3)`, snap.id, login, hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.mfa_totp(user_id,ciphertext,key_version,last_counter) VALUES($1,$2,$3,-1)`, snap.id, ciphertext, s.config.KeyVersion); err != nil {
		return err
	}
	if err := seedBootstrapRoles(ctx, tx, snap.id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.security_state SET site=$1,bootstrap_user_id=$2 WHERE id=1`, s.config.Site, snap.id); err != nil {
		return err
	}
	return bootstrapAudit(ctx, tx, "bootstrap.started", snap.id)
}

func (s *BootstrapService) restartTx(ctx context.Context, tx *sql.Tx, pending string, snap bootstrapSnapshot, ciphertext []byte) error {
	if pending != snap.id {
		return fmt.Errorf("bootstrap state is not the pending operator account")
	}
	if err := checkPendingBootstrap(ctx, tx, snap.id); err != nil {
		return err
	}
	var currentHash, state string
	var version int64
	var old []byte
	var keyVersion int
	var confirmed sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT u.password_hash,u.state,u.credential_version,m.ciphertext,m.key_version,m.confirmed_at FROM public.users u JOIN public.mfa_totp m ON m.user_id=u.id WHERE u.id=$1`, snap.id).Scan(&currentHash, &state, &version, &old, &keyVersion, &confirmed); err != nil {
		return err
	}
	if keyVersion != s.config.KeyVersion {
		return fmt.Errorf("encryption key unavailable")
	}
	if confirmed.Valid || state != "pending" || version != snap.version || currentHash != snap.hash {
		return errBootstrapDenied
	}
	if _, err := decryptSeed(s.config.EncryptionKey, s.config.Site, snap.id, keyVersion, old); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.mfa_totp SET ciphertext=$2,key_version=$3,last_counter=-1 WHERE user_id=$1 AND confirmed_at IS NULL`, snap.id, ciphertext, s.config.KeyVersion); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.users SET credential_version=credential_version+1,version=version+1 WHERE id=$1`, snap.id); err != nil {
		return err
	}
	return bootstrapAudit(ctx, tx, "bootstrap.restarted", snap.id)
}

func attachBootstrapAdmin(ctx context.Context, tx *sql.Tx, user string) error {
	if err := checkPendingBootstrap(ctx, tx, user); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO public.user_roles(user_id,role_id) SELECT $1,id FROM public.roles WHERE name_key='Site Admin' AND state='active'`, user)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("bootstrap seed role missing")
	}
	return checkBootstrapAdmin(ctx, tx, user)
}

func (s *BootstrapService) completeTx(ctx context.Context, tx *sql.Tx, snap bootstrapSnapshot, code string, digests [][]byte) error {
	pending, err := s.state(ctx, tx)
	if err != nil {
		return err
	}
	if pending != snap.id {
		return fmt.Errorf("pending bootstrap account mismatch")
	}
	var hash, state string
	var version, last int64
	var keyVersion int
	var encrypted []byte
	var confirmed sql.NullTime
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT u.password_hash,u.state,u.credential_version,m.ciphertext,m.key_version,m.confirmed_at,m.last_counter,clock_timestamp() FROM public.users u JOIN public.mfa_totp m ON m.user_id=u.id WHERE u.id=$1`, snap.id).Scan(&hash, &state, &version, &encrypted, &keyVersion, &confirmed, &last, &now); err != nil {
		return err
	}
	if keyVersion != s.config.KeyVersion {
		return fmt.Errorf("encryption key unavailable")
	}
	if hash != snap.hash || version != snap.version || state != "pending" || confirmed.Valid {
		return errBootstrapDenied
	}
	if err := attachBootstrapAdmin(ctx, tx, snap.id); err != nil {
		return err
	}
	secret, err := decryptSeed(s.config.EncryptionKey, s.config.Site, snap.id, keyVersion, encrypted)
	if err != nil {
		return err
	}
	counter, err := acceptedCounter(secret, code, now, last)
	if err != nil {
		return err
	}
	return finishBootstrap(ctx, tx, snap.id, now, counter, digests)
}

func (s *BootstrapService) prepareStart(ctx context.Context, login, password string) (bootstrapSnapshot, bool, error) {
	snap, err := s.snapshot(ctx, login)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return bootstrapSnapshot{}, false, err
	}
	var hash string
	if exists {
		valid, err := bootstrapVerify(ctx, password, snap.hash)
		if err != nil {
			return bootstrapSnapshot{}, false, err
		}
		if !valid {
			return snap, true, errBootstrapDenied
		}
		hash = snap.hash
	} else {
		hash, err = bootstrapHash(ctx, password)
		if err != nil {
			return bootstrapSnapshot{}, false, err
		}
		snap.id, err = randomID()
		if err != nil {
			return bootstrapSnapshot{}, false, err
		}
	}
	snap.hash = hash
	return snap, exists, nil
}

func finishBootstrap(ctx context.Context, tx *sql.Tx, user string, now time.Time, counter int64, digests [][]byte) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.mfa_totp SET confirmed_at=$2,last_counter=$3 WHERE user_id=$1`, user, now, counter); err != nil {
		return err
	}
	for _, digest := range digests {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.recovery_codes(user_id,digest) VALUES($1,$2)`, user, digest); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.users SET state='active',enrollment_completed_at=$2,version=version+1 WHERE id=$1`, user, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.security_state SET bootstrap_completed=true WHERE id=1`); err != nil {
		return err
	}
	return bootstrapAudit(ctx, tx, "bootstrap.completed", user)
}
