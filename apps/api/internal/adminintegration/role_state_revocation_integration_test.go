package adminintegration

import (
	"context"
	"testing"
	"time"

	"stepolonia/apps/api/internal/identity"
)

type roleStateRecipient struct {
	f                authFixtureState
	admin, recipient identity.SessionTokens
	role             string
}

func roleStateActiveRecipient(t *testing.T) roleStateRecipient {
	t.Helper()
	ctx := context.Background()
	f := newRestrictedAuthHTTPFixture(t).state
	admin := authFull(t, f)
	account := lifecycleEnrolled(t, f, admin, lifecycleLogin)
	_, err := f.service.ActivateUser(ctx, authReq(admin), account.user.ID, account.user.Version)
	requireNoError(t, err)
	role := createAssignedTestRole(t, f, "Managed policy", lifecycleHTTPActiveState, account.user.ID)
	other := createAssignedTestRole(t, f, "Independent reader", lifecycleHTTPActiveState, account.user.ID)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.read'),(gen_random_uuid(),$1,'roles.create'),(gen_random_uuid(),$2,'users.read')`, role, other)
	requireNoError(t, err)
	return roleStateRecipient{f, admin, lifecycleLoginFull(t, f, account), role}
}

func TestAuthRoleStateAuditRollbackIntegration(t *testing.T) {
	x := roleStateActiveRecipient(t)
	ctx := context.Background()
	_, err := x.f.db.Exec(`CREATE FUNCTION public.fail_disable_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='roles.disabled' THEN RAISE EXCEPTION 'unavailable audit'; END IF; RETURN NEW; END $$;
CREATE TRIGGER fail_disable_audit BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.fail_disable_audit()`)
	requireNoError(t, err)
	_, err = x.f.service.DisableRole(ctx, authReq(x.admin), x.role, 1)
	onboardingError(t, err, identity.ErrSecurityUnavailable)
	role, err := x.f.service.GetRole(ctx, authReq(x.recipient), x.role)
	requireNoError(t, err)
	if role.Version != 1 || role.State != lifecycleHTTPActiveState {
		t.Fatal("audit failure committed state/version")
	}
}

func TestAuthRoleStateMutexIntegration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		createFirst bool
	}{{"disable first", false}, {"create first", true}} {
		t.Run(tc.name, func(t *testing.T) {
			x := roleStateActiveRecipient(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			lock := holdInstallationWriteMutex(t, x.f.db)
			disable := func() error { _, err := x.f.service.DisableRole(ctx, authReq(x.admin), x.role, 1); return err }
			create := func() error {
				_, err := x.f.service.CreateRole(ctx, authReq(x.recipient), rolesInput("Waiting creator"))
				return err
			}
			before, after := disable, create
			if tc.createFirst {
				before, after = create, disable
			}
			a, b := make(chan error, 1), make(chan error, 1)
			go func() { a <- before() }()
			waitForInstallationWriteWaiters(t, x.f.db, 1)
			go func() { b <- after() }()
			waitForInstallationWriteWaiters(t, x.f.db, 2)
			requireNoError(t, lock.Commit())
			requireNoError(t, <-a)
			if !tc.createFirst {
				onboardingError(t, <-b, identity.ErrPermissionDenied)
			} else {
				requireNoError(t, <-b)
			}
			var committed bool
			requireNoError(t, x.f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.roles WHERE name='Waiting creator')`).Scan(&committed))
			if committed != tc.createFirst {
				t.Fatal("protected write committed outside its authorization window")
			}
			_, err := x.f.service.GetRole(ctx, authReq(x.recipient), x.role)
			onboardingError(t, err, identity.ErrPermissionDenied)
			_, err = x.f.service.ListUsers(ctx, authReq(x.recipient), identity.UserPage{})
			requireNoError(t, err)
		})
	}
}

func TestAuthRoleStateRevokeFirstIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := x.f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.restore')`, x.managerRole)
	requireNoError(t, err)
	lock := holdInstallationWriteMutex(t, x.f.db)
	_, err = lock.ExecContext(ctx, `DELETE FROM public.role_grants WHERE role_id=$1 AND operation='roles.restore'`, x.managerRole)
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() { _, err := x.f.service.RestoreRole(ctx, authReq(x.actor), x.targetRole, 1); result <- err }()
	waitForInstallationWriteWaiters(t, x.f.db, 1)
	requireNoError(t, lock.Commit())
	onboardingError(t, <-result, identity.ErrPermissionDenied)
	var unchanged bool
	requireNoError(t, x.f.db.QueryRow(`SELECT state='archived' AND version=1 FROM public.roles WHERE id=$1`, x.targetRole).Scan(&unchanged))
	if !unchanged {
		t.Fatal("waiting restorer retained revoked authority")
	}
}

func TestAuthRoleStateRestorePreservesInactiveAccessIntegration(t *testing.T) {
	x := roleStateActiveRecipient(t)
	ctx := context.Background()
	for i, transition := range []func(context.Context, identity.AuthRequest, string, int64) (identity.Role, error){
		x.f.service.DisableRole, x.f.service.ArchiveRole, x.f.service.RestoreRole,
	} {
		_, err := transition(ctx, authReq(x.admin), x.role, int64(i+1))
		requireNoError(t, err)
		_, err = x.f.service.GetRole(ctx, authReq(x.recipient), x.role)
		onboardingError(t, err, identity.ErrPermissionDenied)
		_, err = x.f.service.ListUsers(ctx, authReq(x.recipient), identity.UserPage{})
		requireNoError(t, err)
	}
	_, err := x.f.service.EnableRole(ctx, authReq(x.admin), x.role, 4)
	requireNoError(t, err)
	_, err = x.f.service.GetRole(ctx, authReq(x.recipient), x.role)
	requireNoError(t, err)
}
