package cmsintegration

import (
	"context"
	"stepolonia/apps/api/internal/cms"
	"testing"
)

const lifecycleLogin = "lifecycle@example.test"

const (
	lifecyclePendingState  = "pending"
	lifecycleDisabledState = "disabled"
)

type lifecycleAccount struct {
	user   cms.User
	secret string
	codes  []string
}

func lifecycleEnrolled(t *testing.T, f authFixtureState, full cms.SessionTokens, login string) lifecycleAccount {
	t.Helper()
	token := onboardingUser(t, f, full, login)
	enrollment := onboardingStart(t, f, token.Token)
	codes, err := f.service.CompleteEnrollment(context.Background(), authReq(enrollment.SessionTokens), bootstrapCode(t, f.db, enrollment.Secret))
	requireNoError(t, err)
	user, err := f.service.GetUser(context.Background(), authReq(full), token.User.ID)
	requireNoError(t, err)
	return lifecycleAccount{user, enrollment.Secret, codes}
}

func lifecycleLoginFull(t *testing.T, f authFixtureState, account lifecycleAccount) cms.SessionTokens {
	t.Helper()
	_, err := f.db.Exec(`UPDATE public.mfa_totp SET last_counter=last_counter-1 WHERE user_id=$1`, account.user.ID)
	requireNoError(t, err)
	pre, err := f.service.NewPreauth(context.Background())
	requireNoError(t, err)
	login, err := f.service.Login(context.Background(), authReq(pre), account.user.Login, bootstrapTestPassphrase)
	requireNoError(t, err)
	full, err := f.service.VerifyMFA(context.Background(), authReq(login), bootstrapCode(t, f.db, account.secret))
	requireNoError(t, err)
	return full
}

func TestAuthLifecycleTransitionsIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	account := lifecycleEnrolled(t, f, full, lifecycleLogin)
	_, err := f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version-1)
	onboardingError(t, err, cms.ErrVersionConflict)
	active, err := f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version)
	requireNoError(t, err)
	if active.State != "active" || active.Version != account.user.Version+1 {
		t.Fatal("activation state/version")
	}
	_, err = f.service.ActivateUser(ctx, authReq(full), active.ID, active.Version)
	onboardingError(t, err, cms.ErrInvalidTransition)
	disabled, err := f.service.DisableUser(ctx, authReq(full), active.ID, active.Version)
	requireNoError(t, err)
	if disabled.State != lifecycleDisabledState || disabled.Version != active.Version+1 {
		t.Fatal("disable state/version")
	}
	_, err = f.service.DisableUser(ctx, authReq(full), disabled.ID, disabled.Version)
	onboardingError(t, err, cms.ErrInvalidTransition)
	var clean bool
	requireNoError(t, f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=$1) AND count(*)=2 FROM public.security_audit WHERE target_id=$1 AND operation IN ('users.activated','users.disabled') AND result='success'`, active.ID).Scan(&clean))
	if !clean {
		t.Fatal("lifecycle created memberships or missed audit")
	}
}

func TestAuthLifecycleReadProjectionIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	account := lifecycleEnrolled(t, f, full, lifecycleLogin)
	var err error
	account.user, err = f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version)
	requireNoError(t, err)
	viewer := lifecycleLoginFull(t, f, account)
	_, err = f.service.CreateUser(ctx, authReq(full), "a-pending@example.test")
	requireNoError(t, err)
	_ = lifecycleEnrolled(t, f, full, "b-disabled@example.test")
	_, err = f.db.Exec(`WITH r AS (INSERT INTO public.roles(id,name_key,state) VALUES(gen_random_uuid(),'Read only lifecycle','active') RETURNING id),g AS (INSERT INTO public.role_grants(id,role_id,operation) SELECT gen_random_uuid(),id,'users.read' FROM r) INSERT INTO public.user_roles(user_id,role_id) SELECT $1,id FROM r`, account.user.ID)
	requireNoError(t, err)
	req := authReq(viewer)
	req.CSRF = ""
	expireSessionStepUp(t, f.db, viewer.Token)
	first, err := f.service.ListUsers(ctx, req, cms.UserPage{Limit: 2})
	requireNoError(t, err)
	if len(first.Items) != 2 || first.NextOffset == nil || *first.NextOffset != 2 || first.Items[0].State != lifecyclePendingState || first.Items[1].State != lifecycleDisabledState {
		t.Fatalf("invalid safe filtered page: %+v", first)
	}
	second, err := f.service.ListUsers(ctx, req, cms.UserPage{Limit: 2, Offset: *first.NextOffset})
	requireNoError(t, err)
	if len(second.Items) != 2 || second.NextOffset != nil {
		t.Fatalf("invalid final page: %+v", second)
	}
	for _, u := range append(first.Items, second.Items...) {
		got, err := f.service.GetUser(ctx, req, u.ID)
		requireNoError(t, err)
		if got != u {
			t.Fatal("safe projection differs")
		}
	}
	_, err = f.service.DisableUser(ctx, authReq(viewer), f.user, 1)
	onboardingError(t, err, cms.ErrPermissionDenied)
}

func TestAuthLifecycleGuardsIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	pending, err := f.service.CreateUser(ctx, authReq(full), lifecycleLogin)
	requireNoError(t, err)
	for _, command := range []func(context.Context, cms.AuthRequest, string, int64) (cms.User, error){f.service.ActivateUser, f.service.DisableUser} {
		_, err = command(ctx, authReq(full), pending.ID, pending.Version)
		onboardingError(t, err, cms.ErrInvalidTransition)
		_, err = command(ctx, authReq(full), "00000000-0000-4000-8000-000000000099", 1)
		onboardingError(t, err, cms.ErrNotFound)
	}
	_, err = f.service.DisableUser(ctx, authReq(full), f.user, 2)
	onboardingError(t, err, cms.ErrLastAdminRequired)
	req := authReq(full)
	req.CSRF = "forged-csrf"
	_, err = f.service.ActivateUser(ctx, req, pending.ID, pending.Version)
	onboardingError(t, err, cms.ErrCSRF)
	expireSessionStepUp(t, f.db, full.Token)
	_, err = f.service.DisableUser(ctx, authReq(full), f.user, 2)
	onboardingError(t, err, cms.ErrStepUpRequired)
}

func TestAuthLifecycleAuditRollbackIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	account := lifecycleEnrolled(t, f, full, lifecycleLogin)
	before := snapshotUserSecurityState(t, f.db, account.user.ID)
	_, err := f.db.Exec(`CREATE FUNCTION public.lifecycle_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='users.activated' THEN RAISE EXCEPTION 'test lifecycle audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER lifecycle_audit_failure BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.lifecycle_audit_failure()`)
	requireNoError(t, err)
	_, err = f.service.ActivateUser(ctx, authReq(full), account.user.ID, account.user.Version)
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	if snapshotUserSecurityState(t, f.db, account.user.ID) != before {
		t.Fatal("audit failure committed account mutation or revocation")
	}
}
