package adminintegration

import (
	"context"
	"stepolonia/apps/api/internal/identity"
	"testing"
)

func TestAuthLifecycleRevocationIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	account := lifecycleEnrolled(t, f, full, lifecycleLogin)
	var err error
	account.user, err = f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version)
	requireNoError(t, err)
	old := lifecycleLoginFull(t, f, account)
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	challenge, err := f.service.Login(ctx, authReq(pre), account.user.Login, bootstrapTestPassphrase)
	requireNoError(t, err)
	pre, err = f.service.NewPreauth(ctx)
	requireNoError(t, err)
	recovery, err := f.service.Recover(ctx, authReq(pre), account.user.Login, bootstrapTestPassphrase, account.codes[0])
	requireNoError(t, err)
	lock := holdInstallationWriteMutex(t, f.db)
	disabled, read := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := f.service.DisableUser(ctx, authReq(full), account.user.ID, account.user.Version)
		disabled <- err
	}()
	waitForInstallationWriteWaiters(t, f.db, 1)
	go func() { read <- f.service.WithSession(ctx, authReq(old), false, authAllow) }()
	waitForInstallationWriteWaiters(t, f.db, 2)
	requireNoError(t, lock.Commit())
	requireNoError(t, <-disabled)
	onboardingError(t, <-read, identity.ErrAuthenticationRequired)
	_, err = f.service.VerifyMFA(ctx, authReq(challenge), bootstrapCode(t, f.db, account.secret))
	onboardingError(t, err, identity.ErrAuthenticationRequired)
	_, err = f.service.CompleteRecovery(ctx, authReq(recovery.SessionTokens), bootstrapCode(t, f.db, recovery.Secret))
	onboardingError(t, err, identity.ErrAuthenticationRequired)
	var revoked bool
	requireNoError(t, f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.sessions WHERE user_id=$1 AND revoked_at IS NULL) AND NOT EXISTS(SELECT 1 FROM public.recovery_attempts WHERE user_id=$1 AND ended_at IS NULL) AND NOT EXISTS(SELECT 1 FROM public.enrollment_attempts WHERE user_id=$1 AND ended_at IS NULL) AND NOT EXISTS(SELECT 1 FROM public.onboarding_tokens WHERE user_id=$1 AND consumed_at IS NULL)`, account.user.ID).Scan(&revoked))
	if !revoked {
		t.Fatal("disable retained an authentication capability")
	}
}

func TestAuthLifecycleWaitingPermissionIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	account := lifecycleEnrolled(t, f, full, lifecycleLogin)
	lock := holdInstallationWriteMutex(t, f.db)
	_, err := lock.Exec(`DELETE FROM public.role_grants WHERE operation='users.activate' AND role_id IN (SELECT role_id FROM public.user_roles WHERE user_id=$1)`, f.user)
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() {
		_, err := f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version)
		result <- err
	}()
	waitForInstallationWriteWaiters(t, f.db, 1)
	requireNoError(t, lock.Commit())
	onboardingError(t, <-result, identity.ErrPermissionDenied)
}

func TestAuthLifecycleWaitingSessionIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	lock := holdInstallationWriteMutex(t, f.db)
	_, err := lock.Exec(`UPDATE public.sessions SET revoked_at=clock_timestamp() WHERE token_digest=$1`, authDigest(full.Token))
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() { _, err := f.service.ListUsers(ctx, authReq(full), identity.UserPage{}); result <- err }()
	waitForInstallationWriteWaiters(t, f.db, 1)
	requireNoError(t, lock.Commit())
	onboardingError(t, <-result, identity.ErrAuthenticationRequired)
}

func TestAuthLifecycleDenialBudgetIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	full := authFull(t, f)
	user, err := f.service.CreateUser(context.Background(), authReq(full), lifecycleLogin)
	requireNoError(t, err)
	for i := range 6 {
		_, err = f.service.DisableUser(context.Background(), authReq(full), user.ID, user.Version)
		expected := identity.ErrInvalidTransition
		if i == 5 {
			expected = identity.ErrRateLimited
		}
		onboardingError(t, err, expected)
	}
	var bounded bool
	requireNoError(t, f.db.QueryRow(`SELECT count(*)=5 AND bool_and(target_id IS NULL AND reason='invalid_credentials' AND request_id IS NOT NULL) FROM public.security_audit WHERE operation='users.disabled' AND result='denied'`).Scan(&bounded))
	if !bounded {
		t.Fatal("denied audit exceeded budget or leaked target")
	}
}

func TestAuthLifecycleSelfDisableSuccessorIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	successor := lifecycleEnrolled(t, f, full, lifecycleLogin)
	_, err := f.db.Exec(`INSERT INTO public.user_roles(user_id,role_id) SELECT $1,role_id FROM public.user_roles WHERE user_id=$2`, successor.user.ID, f.user)
	requireNoError(t, err)
	_, err = f.service.ActivateUser(ctx, authReq(full), successor.user.ID, successor.user.Version)
	requireNoError(t, err)
	self, err := f.service.GetUser(ctx, authReq(full), f.user)
	requireNoError(t, err)
	_, err = f.service.DisableUser(ctx, authReq(full), f.user, self.Version)
	requireNoError(t, err)
	onboardingError(t, f.service.WithSession(ctx, authReq(full), false, authAllow), identity.ErrAuthenticationRequired)
}

func TestAuthLifecycleDisableAuditRollbackIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	account := lifecycleEnrolled(t, f, full, lifecycleLogin)
	var err error
	account.user, err = f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version)
	requireNoError(t, err)
	old := lifecycleLoginFull(t, f, account)
	before := snapshotUserSecurityState(t, f.db, account.user.ID)
	_, err = f.db.Exec(`CREATE FUNCTION public.lifecycle_disable_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='users.disabled' THEN RAISE EXCEPTION 'test disable audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER lifecycle_disable_audit_failure BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.lifecycle_disable_audit_failure()`)
	requireNoError(t, err)
	_, err = f.service.DisableUser(ctx, authReq(full), account.user.ID, account.user.Version)
	onboardingError(t, err, identity.ErrSecurityUnavailable)
	if snapshotUserSecurityState(t, f.db, account.user.ID) != before {
		t.Fatal("audit failure committed revocation or disabled account")
	}
	requireNoError(t, f.service.WithSession(ctx, authReq(old), false, authAllow))
}
