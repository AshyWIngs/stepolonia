package adminintegration

import (
	"context"
	"database/sql"
	"errors"
	"github.com/pquerna/otp/totp"
	"net/netip"
	"stepolonia/apps/api/internal/identity"
	"strings"
	"sync"
	"testing"
	"time"
)

const fullSessionFixtureKind = "full"

func TestAuthLoginSessionRotationIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	login := authLogin(t, f)
	authDenied(t, f.service.WithSession(ctx, authReq(login), true, authAllow))
	bad := authReq(login)
	bad.CSRF = "forged"
	_, err := f.service.VerifyMFA(ctx, bad, bootstrapCode(t, f.db, f.secret))
	if !errors.Is(err, identity.ErrCSRF) {
		t.Fatalf("CSRF: %v", err)
	}
	full, err := f.service.VerifyMFA(ctx, authReq(login), bootstrapCode(t, f.db, f.secret))
	requireNoError(t, err)
	if full.Token == login.Token || full.CSRF == login.CSRF {
		t.Fatal("login fixation")
	}
	_, err = f.service.VerifyMFA(ctx, authReq(login), bootstrapCode(t, f.db, f.secret))
	authDenied(t, err)
	requireNoError(t, f.service.WithSession(ctx, authReq(full), true, func(ctx context.Context, tx *sql.Tx, x identity.Session) error {
		if x.UserID() != f.user || !x.FreshStepUp() {
			t.Fatal("unverified session identity")
		}
		return nil
	}))
	_, err = f.service.StepUp(ctx, authReq(full), bootstrapTestPassphrase, bootstrapCode(t, f.db, f.secret))
	if !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("TOTP replay: %v", err)
	}
	var now time.Time
	requireNoError(t, f.db.QueryRow(`SELECT clock_timestamp()`).Scan(&now))
	code, err := totp.GenerateCode(f.secret, now.Add(30*time.Second))
	requireNoError(t, err)
	stepped, err := f.service.StepUp(ctx, authReq(full), bootstrapTestPassphrase, code)
	requireNoError(t, err)
	authDenied(t, f.service.WithSession(ctx, authReq(full), false, authAllow))
	if stepped.Token == full.Token || stepped.CSRF == full.CSRF {
		t.Fatal("step up fixation")
	}
	requireNoError(t, f.service.Logout(ctx, authReq(stepped), fullSessionFixtureKind))
	authDenied(t, f.service.WithSession(ctx, authReq(stepped), false, authAllow))
	var text string
	requireNoError(t, f.db.QueryRow(`SELECT string_agg(row_to_json(a)::text,' ') FROM public.security_audit a`).Scan(&text))
	for _, secret := range []string{full.Token, full.CSRF, bootstrapTestPassphrase, f.secret, f.codes[0]} {
		if strings.Contains(text, secret) {
			t.Fatal("audit secret leak")
		}
	}
}
func TestAuthRecoveryAtomicIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	recovery, err := f.service.Recover(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[0])
	requireNoError(t, err)
	authDenied(t, f.service.WithSession(ctx, authReq(recovery.SessionTokens), false, authAllow))
	_, err = f.service.StepUp(ctx, authReq(recovery.SessionTokens), bootstrapTestPassphrase, bootstrapCode(t, f.db, recovery.Secret))
	authDenied(t, err)
	var unused bool
	requireNoError(t, f.db.QueryRow(`SELECT consumed_at IS NULL FROM public.recovery_codes WHERE digest=$1`, authDigest(f.codes[0])).Scan(&unused))
	if !unused {
		t.Fatal("reservation consumed code")
	}
	// A write failing at the audit boundary must preserve the previous factors.
	_, err = f.db.Exec(`CREATE FUNCTION public.test_recovery_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='mfa.recovery_completed' THEN RAISE EXCEPTION 'test audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_recovery_audit_failure BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.test_recovery_audit_failure()`)
	requireNoError(t, err)
	_, err = f.service.CompleteRecovery(ctx, authReq(recovery.SessionTokens), bootstrapCode(t, f.db, recovery.Secret))
	if !errors.Is(err, identity.ErrSecurityUnavailable) {
		t.Fatalf("audit failure: %v", err)
	}
	requireNoError(t, f.db.QueryRow(`SELECT consumed_at IS NULL FROM public.recovery_codes WHERE digest=$1`, authDigest(f.codes[0])).Scan(&unused))
	if !unused {
		t.Fatal("audit rollback spent old code")
	}
	requireNoError(t, f.service.WithSession(ctx, authReq(full), false, authAllow))
	_, err = f.db.Exec(`DROP TRIGGER test_recovery_audit_failure ON public.security_audit;DROP FUNCTION public.test_recovery_audit_failure()`)
	requireNoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	code := bootstrapCode(t, f.db, recovery.Secret)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes, err := f.service.CompleteRecovery(ctx, authReq(recovery.SessionTokens), code)
			if err == nil && len(codes) != 10 {
				err = errors.New("invalid new code set")
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("completion commits: %d", success)
	}
	authDenied(t, f.service.WithSession(ctx, authReq(full), false, authAllow))
	authDenied(t, f.service.WithSession(ctx, authReq(recovery.SessionTokens), false, authAllow))
	requireNoError(t, f.db.QueryRow(`SELECT consumed_at IS NOT NULL FROM public.recovery_codes WHERE digest=$1`, authDigest(f.codes[0])).Scan(&unused))
	if !unused {
		t.Fatal("old code still usable")
	}
}

func TestAuthPersistentLimitsAndKeyIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	for i := 0; i < 5; i++ {
		_, err = f.service.Login(ctx, authReq(pre), bootstrapTestLogin, "incorrect password")
		if !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	second, err := identity.NewAuthService(f.db, f.config)
	requireNoError(t, err)
	_, err = second.Login(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase)
	if !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("persistent budget bypass: %v", err)
	}
	bad := f.config
	bad.EncryptionKey = make([]byte, 32)
	wrong, err := identity.NewAuthService(f.db, bad)
	requireNoError(t, err)
	if !errors.Is(wrong.CheckReady(ctx), identity.ErrSecurityUnavailable) {
		t.Fatal("wrong key did not fail startup")
	}
	bad = f.config
	bad.Site = "other.test"
	wrong, err = identity.NewAuthService(f.db, bad)
	requireNoError(t, err)
	if !errors.Is(wrong.CheckReady(ctx), identity.ErrSecurityUnavailable) {
		t.Fatal("wrong installation did not fail startup")
	}
}
func TestAuthChallengeAndExpiryIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	login := authLogin(t, f)
	for i := 0; i < 5; i++ {
		_, err := f.service.VerifyMFA(ctx, authReq(login), "invalid")
		if !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("MFA attempt %d: %v", i, err)
		}
	}
	_, err := f.service.VerifyMFA(ctx, authReq(login), bootstrapCode(t, f.db, f.secret))
	authDenied(t, err)
	// Clear only this fixture's budgets to exercise independently expired full sessions.
	requireNoError(t, identity.WithWriteMutex(ctx, f.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM public.auth_attempt_buckets`)
		return err
	}))
	full := authFull(t, f)
	requireNoError(t, identity.WithWriteMutex(ctx, f.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE public.sessions SET expires_at=expires_at-interval '31 minutes',created_at=created_at-interval '31 minutes',last_seen=last_seen-interval '31 minutes',step_up_at=step_up_at-interval '31 minutes' WHERE token_digest=$1`, authDigest(full.Token))
		return err
	}))
	authDenied(t, f.service.WithSession(ctx, authReq(full), false, authAllow))
}
func TestAuthRecoveryReservationCancelExpiryIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	r, err := f.service.Recover(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[0])
	requireNoError(t, err)
	pre2, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	_, err = f.service.Recover(ctx, authReq(pre2), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[1])
	if !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("parallel reservation: %v", err)
	}
	requireNoError(t, f.service.CancelRecovery(ctx, authReq(r.SessionTokens)))
	r, err = f.service.Recover(ctx, authReq(pre2), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[0])
	requireNoError(t, err)
	requireNoError(t, identity.WithWriteMutex(ctx, f.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE public.recovery_attempts SET created_at=created_at-interval '11 minutes',expires_at=expires_at-interval '11 minutes' WHERE ended_at IS NULL`)
		return err
	}))
	pre3, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	_, err = f.service.Recover(ctx, authReq(pre3), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[0])
	requireNoError(t, err)
	var n int
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.recovery_codes WHERE consumed_at IS NOT NULL`).Scan(&n))
	if n != 0 {
		t.Fatal("cancel/expiry consumed code")
	}
}

func TestAuthConcurrentMFAReplayIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	a, b := authLogin(t, f), authLogin(t, f)
	code := bootstrapCode(t, f.db, f.secret)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, challenge := range []identity.SessionTokens{a, b} {
		wg.Add(1)
		go func(token identity.SessionTokens) {
			defer wg.Done()
			_, err := f.service.VerifyMFA(ctx, authReq(token), code)
			results <- err
		}(challenge)
	}
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, identity.ErrInvalidCredentials) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatalf("MFA race success=%d denied=%d", success, denied)
	}
}
func TestAuthPreauthIssuanceIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	req := identity.AuthRequest{ClientIP: netip.MustParseAddr("192.0.2.42")}
	for i := 0; i < 30; i++ {
		_, err := f.service.NewPreauthFor(ctx, req)
		requireNoError(t, err)
	}
	_, err := f.service.NewPreauthFor(ctx, req)
	if !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("issuance limit: %v", err)
	}
	// Authentication failure budgets are independent from GET issuance budgets.
	var count int
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.auth_attempt_buckets WHERE kind IN ('account','ip','site')`).Scan(&count))
	if count != 0 {
		t.Fatal("issuance polluted failure counters")
	}
}
