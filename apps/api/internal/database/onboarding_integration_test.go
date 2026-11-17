package database

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/cms"
)

const onboardingDeniedLogin = "denied@example.test"
const onboardingEnrollmentKind = "enrollment"

func TestAuthOnboardingUnavailableTargetIntegration(t *testing.T) {
	f := qaOnboardingFixture(t)
	full := authFull(t, f.state)
	ctx := context.Background()
	u, err := f.state.service.CreateUser(ctx, authReq(full), "unavailable-target@example.test")
	requireNoError(t, err)
	_, err = f.state.db.Exec(`INSERT INTO public.user_roles(user_id,role_id) SELECT $1,role_id FROM public.user_roles WHERE user_id=$2 LIMIT 1`, u.ID, f.state.user)
	requireNoError(t, err)
	for _, target := range []string{u.ID, "00000000-0000-4000-8000-000000000001"} {
		_, err = f.state.service.IssueOnboardingToken(ctx, authReq(full), target, u.Version)
		onboardingError(t, err, cms.ErrNotFound)
	}
	var unchanged bool
	requireNoError(t, f.state.db.QueryRow(`SELECT version=$2 AND NOT EXISTS(SELECT 1 FROM public.onboarding_tokens WHERE user_id=u.id) FROM public.users u WHERE id=$1`, u.ID, u.Version).Scan(&unchanged))
	if !unchanged {
		t.Fatal("unavailable target provisioning changed its version or issued a token")
	}
}

func onboardingUser(t *testing.T, f authFixtureState, full cms.SessionTokens, login string) cms.OnboardingToken {
	t.Helper()
	ctx := context.Background()
	u, err := f.service.CreateUser(ctx, authReq(full), login)
	requireNoError(t, err)
	if u.State != "pending" || u.Version != 1 {
		t.Fatalf("invalid pending result: %+v", u)
	}
	token, err := f.service.IssueOnboardingToken(ctx, authReq(full), u.ID, u.Version)
	requireNoError(t, err)
	if token.User.Version != 2 {
		t.Fatal("issuance did not advance expected version")
	}
	return token
}

func onboardingStart(t *testing.T, f authFixtureState, token string) cms.Enrollment {
	t.Helper()
	pre, err := f.service.NewPreauth(context.Background())
	requireNoError(t, err)
	enrollment, err := f.service.StartEnrollment(context.Background(), authReq(pre), token, bootstrapTestPassphrase)
	requireNoError(t, err)
	return enrollment
}

func onboardingError(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestAuthOnboardingSuccessDisabledIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "NewUser@example.test")
	var before bool
	requireNoError(t, f.db.QueryRow(`SELECT state='pending' AND login_key='newuser@example.test' AND password_hash IS NULL AND enrollment_completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.sessions WHERE user_id=u.id) AND NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=u.id) FROM public.users u WHERE id=$1`, token.User.ID).Scan(&before))
	if !before {
		t.Fatal("pending creation installed credentials, access or sessions")
	}
	var lifetime time.Duration
	var seconds float64
	requireNoError(t, f.db.QueryRow(`SELECT extract(epoch FROM expires_at-created_at) FROM public.onboarding_tokens WHERE user_id=$1`, token.User.ID).Scan(&seconds))
	lifetime = time.Duration(seconds * float64(time.Second))
	if lifetime > 24*time.Hour || lifetime < 24*time.Hour-time.Second {
		t.Fatal("onboarding TTL is not 24h")
	}
	enrollment := onboardingStart(t, f, token.Token)
	if enrollment.Kind != onboardingEnrollmentKind || enrollment.ExpiresAt.After(token.ExpiresAt) || time.Until(enrollment.ExpiresAt) > 10*time.Minute {
		t.Fatal("invalid restricted lifetime")
	}
	onboardingError(t, f.service.WithSession(ctx, authReq(enrollment.SessionTokens), true, authAllow), cms.ErrAuthenticationRequired)
	_, err := f.service.StepUp(ctx, authReq(enrollment.SessionTokens), bootstrapTestPassphrase, bootstrapCode(t, f.db, enrollment.Secret))
	onboardingError(t, err, cms.ErrAuthenticationRequired)
	_, err = f.service.VerifyMFA(ctx, authReq(enrollment.SessionTokens), bootstrapCode(t, f.db, enrollment.Secret))
	onboardingError(t, err, cms.ErrAuthenticationRequired)
	code := bootstrapCode(t, f.db, enrollment.Secret)
	codes, err := f.service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), code)
	requireNoError(t, err)
	if len(codes) != 10 {
		t.Fatal("recovery code count")
	}
	var after bool
	requireNoError(t, f.db.QueryRow(`SELECT u.state='disabled' AND u.version=3 AND u.credential_version=3 AND u.password_hash LIKE '$argon2id$%' AND u.enrollment_completed_at IS NOT NULL AND m.confirmed_at IS NOT NULL AND m.last_counter>=0 AND NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=u.id) AND NOT EXISTS(SELECT 1 FROM public.sessions WHERE user_id=u.id AND revoked_at IS NULL) FROM public.users u JOIN public.mfa_totp m ON m.user_id=u.id WHERE u.id=$1`, token.User.ID).Scan(&after))
	if !after {
		t.Fatal("enrollment did not leave fully enrolled disabled account")
	}
	for _, code := range codes {
		d := sha256.Sum256([]byte(code))
		var n int
		requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.recovery_codes WHERE user_id=$1 AND digest=$2 AND consumed_at IS NULL`, token.User.ID, d[:]).Scan(&n))
		if n != 1 {
			t.Fatal("missing recovery digest")
		}
	}
	_, err = f.service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), code)
	onboardingError(t, err, cms.ErrAuthenticationRequired)
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	_, err = f.service.Login(ctx, authReq(pre), token.User.Login, bootstrapTestPassphrase)
	onboardingError(t, err, cms.ErrInvalidCredentials)
	_, err = f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, 3)
	onboardingError(t, err, cms.ErrInvalidTransition)
	assertOnboardingArtifacts(t, f, token, enrollment, codes)

}

func assertOnboardingArtifacts(t *testing.T, f authFixtureState, token cms.OnboardingToken, enrollment cms.Enrollment, codes []string) {
	t.Helper()
	var properAudit bool
	requireNoError(t, f.db.QueryRow(`SELECT count(*)=2 FROM public.security_audit WHERE operation IN ('users.created','users.onboarding_issued') AND result='success' AND actor_id=$1 AND target_id=$2`, f.user, token.User.ID).Scan(&properAudit))
	if !properAudit {
		t.Fatal("issuer audit identity mismatch")
	}
	var stored string
	requireNoError(t, f.db.QueryRow(`SELECT COALESCE(json_agg(a)::text,'') FROM public.security_audit a`).Scan(&stored))
	for _, secret := range []string{token.Token, enrollment.Token, enrollment.CSRF, enrollment.Secret, bootstrapTestPassphrase, codes[0]} {
		if strings.Contains(stored, secret) {
			t.Fatal("audit plaintext secret")
		}
	}
	var rawToken []byte
	requireNoError(t, f.db.QueryRow(`SELECT digest FROM public.onboarding_tokens WHERE user_id=$1`, token.User.ID).Scan(&rawToken))
	d := sha256.Sum256([]byte(token.Token))
	if string(d[:]) != string(rawToken) {
		t.Fatal("token not digest-only")
	}
}

func TestAuthOnboardingAuthorizationIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	for _, req := range []cms.AuthRequest{{ClientIP: authReq(full).ClientIP}, authReq(authLogin(t, f))} {
		_, err := f.service.CreateUser(ctx, req, onboardingDeniedLogin)
		onboardingError(t, err, cms.ErrAuthenticationRequired)
	}
	bad := authReq(full)
	bad.CSRF = "forged"
	_, err := f.service.CreateUser(ctx, bad, onboardingDeniedLogin)
	onboardingError(t, err, cms.ErrCSRF)
	_, err = f.db.Exec(`UPDATE public.sessions SET step_up_at=NULL WHERE kind='full'`)
	requireNoError(t, err)
	_, err = f.service.CreateUser(ctx, authReq(full), onboardingDeniedLogin)
	onboardingError(t, err, cms.ErrStepUpRequired)
	_, err = f.db.Exec(`UPDATE public.sessions SET step_up_at=last_seen WHERE kind='full'; UPDATE public.roles SET name_key='Ordinary provisioning role' WHERE name_key='Site Admin'; UPDATE public.role_grants SET can_delegate=false WHERE operation='users.create'`)
	requireNoError(t, err)
	u, err := f.service.CreateUser(ctx, authReq(full), "allowed@example.test")
	requireNoError(t, err)
	_, err = f.db.Exec(`UPDATE public.roles SET state='disabled' WHERE name_key='Ordinary provisioning role'`)
	requireNoError(t, err)
	_, err = f.service.CreateUser(ctx, authReq(full), "denied@example.test")
	onboardingError(t, err, cms.ErrPermissionDenied)
	_, err = f.service.IssueOnboardingToken(ctx, authReq(full), u.ID, u.Version)
	onboardingError(t, err, cms.ErrPermissionDenied)
	_, err = f.db.Exec(`UPDATE public.roles SET state='active' WHERE name_key='Ordinary provisioning role';DELETE FROM public.role_grants WHERE operation='users.create'`)
	requireNoError(t, err)
	_, err = f.service.IssueOnboardingToken(ctx, authReq(full), u.ID, u.Version)
	onboardingError(t, err, cms.ErrPermissionDenied)
	var n int
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.users WHERE login_key='denied@example.test'`).Scan(&n))
	if n != 0 {
		t.Fatal("denied command wrote user")
	}
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.security_audit WHERE operation='users.created' AND result='denied'`).Scan(&n))
	if n < 5 {
		t.Fatal("denials were not audited")
	}
}

func TestAuthOnboardingReissueExpiryVersionIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "reissue@example.test")
	_, err := f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, 1)
	onboardingError(t, err, cms.ErrVersionConflict)
	enrollment := onboardingStart(t, f, token.Token)
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	_, err = f.service.StartEnrollment(ctx, authReq(pre), token.Token, bootstrapTestPassphrase)
	onboardingError(t, err, cms.ErrInvalidCredentials)
	next, err := f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, 2)
	requireNoError(t, err)
	if next.Token == token.Token || next.User.Version != 3 {
		t.Fatal("reissue did not rotate")
	}
	_, err = f.service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), bootstrapCode(t, f.db, enrollment.Secret))
	onboardingError(t, err, cms.ErrAuthenticationRequired)
	_, err = f.db.Exec(`UPDATE public.onboarding_tokens SET created_at=clock_timestamp()-interval '25 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE user_id=$1 AND consumed_at IS NULL`, token.User.ID)
	requireNoError(t, err)
	_, err = f.service.StartEnrollment(ctx, authReq(pre), next.Token, bootstrapTestPassphrase)
	onboardingError(t, err, cms.ErrInvalidCredentials)
	next, err = f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, 3)
	requireNoError(t, err)
	enrollment = onboardingStart(t, f, next.Token)
	_, err = f.db.Exec(`UPDATE public.users SET credential_version=credential_version+1 WHERE id=$1`, token.User.ID)
	requireNoError(t, err)
	_, err = f.service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), bootstrapCode(t, f.db, enrollment.Secret))
	onboardingError(t, err, cms.ErrAuthenticationRequired)
	var n int
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.mfa_totp WHERE user_id=$1`, token.User.ID).Scan(&n))
	if n != 0 {
		t.Fatal("invalidated enrollment installed factors")
	}
}

func TestAuthOnboardingConcurrentSingleUseIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "concurrent@example.test")
	var pre [2]cms.SessionTokens
	for i := range pre {
		var err error
		pre[i], err = f.service.NewPreauth(ctx)
		requireNoError(t, err)
	}
	var results [2]cms.Enrollment
	var errs [2]error
	var wg sync.WaitGroup
	for i := range pre {
		wg.Go(func() {
			results[i], errs[i] = f.service.StartEnrollment(ctx, authReq(pre[i]), token.Token, bootstrapTestPassphrase)
		})
	}
	wg.Wait()
	var winner *cms.Enrollment
	for i, err := range errs {
		if err == nil {
			if winner != nil {
				t.Fatal("concurrent token starts both succeeded")
			}
			winner = &results[i]
		} else if !errors.Is(err, cms.ErrInvalidCredentials) {
			t.Fatalf("unexpected start loser: %v", err)
		}
	}
	if winner == nil {
		t.Fatal("no enrollment start succeeded")
	}
	e := *winner
	code := bootstrapCode(t, f.db, e.Secret)
	assertOnboardingConcurrentCompletion(t, f, e, code)

}

func assertOnboardingConcurrentCompletion(t *testing.T, f authFixtureState, e cms.Enrollment, code string) {
	t.Helper()
	ctx := context.Background()
	var errs [2]error
	var wg sync.WaitGroup
	var codes [2][]string
	for i := range errs {
		wg.Go(func() { codes[i], errs[i] = f.service.CompleteEnrollment(ctx, authReq(e.SessionTokens), code) })
	}
	wg.Wait()
	success := 0
	for i, err := range errs {
		if err == nil {
			success++
			if len(codes[i]) != 10 {
				t.Fatal("winning completion codes")
			}
		} else {
			onboardingError(t, err, cms.ErrAuthenticationRequired)
			if len(codes[i]) != 0 {
				t.Fatal("loser received secrets")
			}
		}
	}
	if success != 1 {
		t.Fatal("completion was not single-use")
	}
}

func TestAuthOnboardingAuditRollbackIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	_, err := f.db.Exec(`CREATE FUNCTION public.reject_onboarding_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation IN ('users.created','users.onboarding_issued','auth.enrollment_started','auth.enrollment_completed') THEN RAISE EXCEPTION 'test onboarding audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_onboarding_audit BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.reject_onboarding_audit()`)
	requireNoError(t, err)
	u, err := f.service.CreateUser(ctx, authReq(full), "rollback@example.test")
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	if u.ID != "" {
		t.Fatal("failed creation returned user")
	}
	setOnboardingAuditFailure(t, f, false)
	u, err = f.service.CreateUser(ctx, authReq(full), "rollback@example.test")
	requireNoError(t, err)
	setOnboardingAuditFailure(t, f, true)
	token, err := f.service.IssueOnboardingToken(ctx, authReq(full), u.ID, 1)
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	if token.Token != "" {
		t.Fatal("failed issuance exposed token")
	}
	var n int
	requireNoError(t, f.db.QueryRow(`SELECT version FROM public.users WHERE id=$1`, u.ID).Scan(&n))
	if n != 1 {
		t.Fatal("failed issuance bumped version")
	}
	setOnboardingAuditFailure(t, f, false)
	token, err = f.service.IssueOnboardingToken(ctx, authReq(full), u.ID, 1)
	requireNoError(t, err)
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	setOnboardingAuditFailure(t, f, true)
	e, err := f.service.StartEnrollment(ctx, authReq(pre), token.Token, bootstrapTestPassphrase)
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	if e.Secret != "" || e.Token != "" {
		t.Fatal("failed start exposed seed/session")
	}
	setOnboardingAuditFailure(t, f, false)
	e, err = f.service.StartEnrollment(ctx, authReq(pre), token.Token, bootstrapTestPassphrase)
	requireNoError(t, err)
	setOnboardingAuditFailure(t, f, true)
	codes, err := f.service.CompleteEnrollment(ctx, authReq(e.SessionTokens), bootstrapCode(t, f.db, e.Secret))
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	if len(codes) != 0 {
		t.Fatal("failed completion exposed codes")
	}
	var unchanged bool
	requireNoError(t, f.db.QueryRow(`SELECT state='pending' AND password_hash IS NULL AND enrollment_completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.mfa_totp WHERE user_id=u.id) AND NOT EXISTS(SELECT 1 FROM public.recovery_codes WHERE user_id=u.id) AND EXISTS(SELECT 1 FROM public.sessions WHERE user_id=u.id AND kind='enrollment' AND revoked_at IS NULL) FROM public.users u WHERE id=$1`, u.ID).Scan(&unchanged))
	if !unchanged {
		t.Fatal("audit failure left partial credentials")
	}
	setOnboardingAuditFailure(t, f, false)
	_, err = f.service.CompleteEnrollment(ctx, authReq(e.SessionTokens), bootstrapCode(t, f.db, e.Secret))
	requireNoError(t, err)
}

func TestAuthOnboardingSecurityPoolAndConstraintsIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	name := "onboard_" + strings.ReplaceAll(f.user, "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	_, err := f.db.Exec("CREATE ROLE " + quoted + " LOGIN IN ROLE sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = f.db.Exec("DROP ROLE " + quoted) })
	cfg := cmsDisposableDatabaseConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = name
	_, err = f.db.Exec("REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " FROM PUBLIC")
	requireNoError(t, err)
	pool := Open(cfg)
	defer func() { _ = pool.Close() }()
	requireNoError(t, CheckSecurity(ctx, pool))
	f.service, err = cms.NewAuthService(pool, f.config)
	requireNoError(t, err)
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "securitypool@example.test")
	e := onboardingStart(t, f, token.Token)
	_, err = f.service.CompleteEnrollment(ctx, authReq(e.SessionTokens), bootstrapCode(t, f.db, e.Secret))
	requireNoError(t, err)
	for _, table := range []struct{ name, deletion string }{{"onboarding_tokens", "DELETE FROM public.onboarding_tokens"}, {"enrollment_attempts", "DELETE FROM public.enrollment_attempts"}} {
		var comment string
		requireNoError(t, f.db.QueryRow(`SELECT obj_description(('public.'||$1)::regclass,'pg_class')`, table.name).Scan(&comment))
		if comment == "" {
			t.Fatal("missing security metadata")
		}
		for _, role := range []string{"sp_publisher", "sp_language_importer"} {
			var access bool
			requireNoError(t, f.db.QueryRow(`SELECT has_table_privilege($1,'public.'||$2,'SELECT,INSERT,UPDATE,DELETE')`, role, table.name).Scan(&access))
			if access {
				t.Fatal("public publisher can access onboarding")
			}
		}
		_, err = pool.Exec(table.deletion)
		assertSQLState(t, err, "42501")
	}
	requireNoError(t, CheckSchema(ctx, f.db))
}

func setOnboardingAuditFailure(t *testing.T, f authFixtureState, enabled bool) {
	t.Helper()
	query := `ALTER TABLE public.security_audit DISABLE TRIGGER reject_onboarding_audit`
	if enabled {
		query = `ALTER TABLE public.security_audit ENABLE TRIGGER reject_onboarding_audit`
	}
	_, err := f.db.Exec(query)
	requireNoError(t, err)
}

func TestAuthOnboardingFactorAdmissionAndIsolationIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "factors@example.test")
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	_, err = f.service.StartEnrollment(ctx, authReq(pre), token.Token, "weak")
	onboardingError(t, err, cms.ErrValidation)
	e, err := f.service.StartEnrollment(ctx, authReq(pre), token.Token, bootstrapTestPassphrase)
	requireNoError(t, err)
	rotated, err := f.service.RotateCSRF(ctx, e.Token, e.Kind)
	requireNoError(t, err)
	_, err = f.service.CompleteEnrollment(ctx, authReq(e.SessionTokens), "not-six-digits")
	onboardingError(t, err, cms.ErrCSRF)
	for range 5 {
		codes, err := f.service.CompleteEnrollment(ctx, authReq(rotated), "not-six-digits")
		onboardingError(t, err, cms.ErrInvalidCredentials)
		if len(codes) != 0 {
			t.Fatal("unconfirmed factor returned recovery secrets")
		}
	}
	_, err = f.service.CompleteEnrollment(ctx, authReq(rotated), bootstrapCode(t, f.db, e.Secret))
	onboardingError(t, err, cms.ErrAuthenticationRequired)
	other := onboardingUser(t, f, full, "other@example.test")
	otherEnrollment := onboardingStart(t, f, other.Token)
	_, err = f.service.CompleteEnrollment(ctx, authReq(otherEnrollment.SessionTokens), bootstrapCode(t, f.db, otherEnrollment.Secret))
	requireNoError(t, err)
	var isolated bool
	requireNoError(t, f.db.QueryRow(`SELECT state='pending' AND password_hash IS NULL AND enrollment_completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.mfa_totp WHERE user_id=u.id) AND NOT EXISTS(SELECT 1 FROM public.recovery_codes WHERE user_id=u.id) FROM public.users u WHERE id=$1`, token.User.ID).Scan(&isolated))
	if !isolated {
		t.Fatal("another session changed the unconfirmed account")
	}
}

func TestAuthOnboardingRevocationAfterMutexWaitIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "revoked@example.test")
	tx, err := f.db.BeginTx(ctx, nil)
	requireNoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, cms.WriteMutexSQL)
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() {
		_, err := f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, token.User.Version)
		result <- err
	}()
	waitOnboardingMutex(t, f)
	_, err = tx.ExecContext(ctx, `UPDATE public.roles SET state='disabled' WHERE id IN(SELECT role_id FROM public.user_roles WHERE user_id=$1)`, f.user)
	requireNoError(t, err)
	requireNoError(t, tx.Commit())
	onboardingError(t, <-result, cms.ErrPermissionDenied)
	var unchanged bool
	requireNoError(t, f.db.QueryRow(`SELECT version=2 AND EXISTS(SELECT 1 FROM public.onboarding_tokens WHERE user_id=u.id AND consumed_at IS NULL) FROM public.users u WHERE id=$1`, token.User.ID).Scan(&unchanged))
	if !unchanged {
		t.Fatal("waiting writer ignored the committed permission revocation")
	}
}

func waitOnboardingMutex(t *testing.T, f authFixtureState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		requireNoError(t, f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid=1398031696 AND objid=1 AND NOT granted)`).Scan(&waiting))
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("writer did not wait on the installation mutex")
}

func TestAuthOnboardingUnauthenticatedAuditBudgetIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	for i := range 8 {
		pre, err := f.service.NewPreauth(ctx)
		requireNoError(t, err)
		// Different valid random bearers must share the same trusted-IP budget.
		_, err = f.service.CreateUser(ctx, authReq(pre), "anonymous-no-write")
		if i < 5 {
			onboardingError(t, err, cms.ErrAuthenticationRequired)
		} else {
			onboardingError(t, err, cms.ErrRateLimited)
		}
	}
	var bounded bool
	requireNoError(t, f.db.QueryRow(`SELECT (SELECT count(*) FROM public.security_audit WHERE operation='users.created' AND result='denied')=5 AND NOT EXISTS(SELECT 1 FROM public.users WHERE login_key='anonymous-no-write') AND (SELECT COALESCE(sum(count),0) FROM public.auth_attempts WHERE kind='account')=5`).Scan(&bounded))
	if !bounded {
		t.Fatal("forged cookie rotation bypassed persistent denial aggregation")
	}
	// Failure saturation must not block a properly authorized full command.
	full := authFull(t, f)
	_, err := f.service.CreateUser(ctx, authReq(full), "authorized-after-denial")
	requireNoError(t, err)
}

func TestAuthOnboardingSQLInvariantsIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	token := onboardingUser(t, f, full, "sql-invariants@example.test")
	for _, scenario := range []struct{ query, state string }{
		{`UPDATE public.onboarding_tokens SET expires_at=created_at+interval '25 hours'`, "23514"},
		{`UPDATE public.onboarding_tokens SET digest=decode(repeat('00',31),'hex')`, "23514"},
		{`INSERT INTO public.onboarding_tokens(digest,user_id,credential_version,created_at,expires_at) SELECT decode(repeat('00',32),'hex'),user_id,credential_version,created_at,expires_at FROM public.onboarding_tokens`, "23505"},
	} {
		tx, err := f.db.BeginTx(ctx, nil)
		requireNoError(t, err)
		_, err = tx.ExecContext(ctx, scenario.query)
		assertSQLState(t, err, scenario.state)
		requireNoError(t, tx.Rollback())
	}
	_ = onboardingStart(t, f, token.Token)
	tx, err := f.db.BeginTx(ctx, nil)
	requireNoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE public.enrollment_attempts SET credential_version=credential_version+1`)
	assertSQLState(t, err, "23503")
	requireNoError(t, tx.Rollback())
	lock, err := f.db.BeginTx(ctx, nil)
	requireNoError(t, err)
	defer func() { _ = lock.Rollback() }()
	_, err = lock.ExecContext(ctx, cms.WriteMutexSQL)
	requireNoError(t, err)
	for _, query := range []string{`UPDATE public.onboarding_tokens SET consumed_at=consumed_at`, `UPDATE public.enrollment_attempts SET ended_at=ended_at`} {
		blocked, err := f.db.BeginTx(ctx, nil)
		requireNoError(t, err)
		_, err = blocked.ExecContext(ctx, "SET LOCAL lock_timeout='100ms'")
		requireNoError(t, err)
		_, err = blocked.ExecContext(ctx, query)
		assertSQLState(t, err, "55P03")
		requireNoError(t, blocked.Rollback())
	}
}
