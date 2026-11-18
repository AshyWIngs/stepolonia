package cmsintegration

import (
	"context"
	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/testpostgres"
	"testing"
	"time"
)

func TestAuthRolesRestrictedACLIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	cfg := testpostgres.CMSConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_auth"
	pool := database.Open(cfg)
	defer func() { _ = pool.Close() }()
	requireNoError(t, database.CheckSecurity(context.Background(), pool))
	for _, query := range []string{
		`INSERT INTO public.roles(id,name_key,state) VALUES(gen_random_uuid(),'illegal state','active')`,
		`INSERT INTO public.roles(id,name_key,version) VALUES(gen_random_uuid(),'illegal version',99)`,
		`UPDATE public.roles SET state='active'`,
		`UPDATE public.roles SET id=gen_random_uuid()`,
		`DELETE FROM public.roles`,
		`UPDATE public.role_grants SET can_delegate=true`,
		`INSERT INTO public.user_roles(user_id,role_id) VALUES(gen_random_uuid(),gen_random_uuid())`,
		`SELECT key FROM public.content_types`,
		`SELECT display_labels FROM public.content_fields`,
		`UPDATE public.content_fields SET key='changed'`,
	} {
		_, err := pool.Exec(query)
		assertSQLState(t, err, "42501")
	}
	_, err := f.db.Exec(`GRANT UPDATE(state) ON public.roles TO sp_security`)
	requireNoError(t, err)
	if database.CheckSecurity(context.Background(), pool) == nil {
		t.Fatal("extra lifecycle column privilege accepted")
	}
	_, err = f.db.Exec(`REVOKE UPDATE(state) ON public.roles FROM sp_security`)
	requireNoError(t, err)
	requireNoError(t, database.CheckSecurity(context.Background(), pool))
}

func TestAuthRolesReadMetadataIntegration(t *testing.T) {
	x := rolesManager(t)
	req := authReq(x.actor)
	req.CSRF = ""
	expireSessionStepUp(t, x.f.db, x.actor.Token)
	// Policy IDs are safe metadata. The reader has no data grants and need not
	// possess the superior role's delegation envelope just to inspect its policy.
	first, err := x.f.service.ListRoles(context.Background(), req, cms.RolePage{Limit: 1})
	requireNoError(t, err)
	if len(first.Items) != 1 || first.NextOffset == nil || first.Items[0].Grants == nil {
		t.Fatal("bounded policy projection missing")
	}
	var admin string
	requireNoError(t, x.f.db.QueryRow(`SELECT role_id::text FROM public.user_roles WHERE user_id=$1`, x.f.user).Scan(&admin))
	got, err := x.f.service.GetRole(context.Background(), req, admin)
	requireNoError(t, err)
	if len(got.Grants) == 0 || got.ID != admin {
		t.Fatal("roles.read incorrectly required edit authority")
	}
	for _, page := range []cms.RolePage{{Limit: 33}, {Offset: 33}, {Limit: -1}, {Offset: -1}} {
		_, err = x.f.service.ListRoles(context.Background(), req, page)
		onboardingError(t, err, cms.ErrValidation)
	}
}

func TestAuthRolesGrantWriteRollbackIntegration(t *testing.T) {
	x := rolesManager(t)
	_, err := x.f.db.Exec(`CREATE FUNCTION public.roles_grant_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test role grant unavailable'; END $$; CREATE TRIGGER roles_grant_failure BEFORE INSERT ON public.grant_fields FOR EACH ROW EXECUTE FUNCTION public.roles_grant_failure()`)
	requireNoError(t, err)
	_, err = x.f.service.EditRole(context.Background(), authReq(x.actor), x.targetRole, 1, rolesInput("Failed grant write", rolesContentGrant()))
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	var intact bool
	requireNoError(t, x.f.db.QueryRow(`SELECT version=1 AND name_key='Latent content' AND (SELECT count(*) FROM public.role_grants WHERE role_id=$1)=1 AND (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON g.id=f.grant_id WHERE g.role_id=$1)=1 FROM public.roles WHERE id=$1`, x.targetRole).Scan(&intact))
	if !intact {
		t.Fatal("failed replacement deleted prior grants or changed version")
	}
}

func TestAuthRolesCapacityIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	full := authFull(t, f)
	_, err := f.db.Exec(`INSERT INTO public.roles(id,name_key) SELECT gen_random_uuid(),'role-cap-'||i FROM generate_series(1,29) i`)
	requireNoError(t, err)
	_, err = f.service.CreateRole(context.Background(), authReq(full), rolesInput("Over capacity"))
	onboardingError(t, err, cms.ErrInvalidTransition)
	_, err = f.db.Exec(`INSERT INTO public.roles(id,name_key) VALUES(gen_random_uuid(),'drift over capacity')`)
	requireNoError(t, err)
	_, err = f.service.ListRoles(context.Background(), authReq(full), cms.RolePage{})
	onboardingError(t, err, cms.ErrSecurityUnavailable)
}

func TestAuthRolesDenialBudgetIntegration(t *testing.T) {
	x := rolesManager(t)
	for i := range 6 {
		_, err := x.f.service.EditRole(context.Background(), authReq(x.actor), x.targetRole, 999, rolesInput("Stale command"))
		expected := cms.ErrVersionConflict
		if i == 5 {
			expected = cms.ErrRateLimited
		}
		onboardingError(t, err, expected)
	}
	var bounded bool
	requireNoError(t, x.f.db.QueryRow(`SELECT count(*)=5 AND bool_and(target_id IS NULL AND reason='invalid_credentials' AND request_id IS NOT NULL) FROM public.security_audit WHERE operation='roles.edited' AND result='denied'`).Scan(&bounded))
	if !bounded {
		t.Fatal("denied role commands leaked target or exceeded audit budget")
	}
}

func TestAuthRolesRevokeFirstIntegration(t *testing.T) {
	x := rolesManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lock := holdInstallationWriteMutex(t, x.f.db)
	_, err := lock.ExecContext(ctx, `DELETE FROM public.role_grants WHERE role_id=$1 AND operation='roles.edit'`, x.managerRole)
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() {
		_, err := x.f.service.EditRole(ctx, authReq(x.actor), x.targetRole, 1, rolesInput("Waiting editor", rolesContentGrant()))
		result <- err
	}()
	waitForInstallationWriteWaiters(t, x.f.db, 1)
	requireNoError(t, lock.Commit())
	onboardingError(t, <-result, cms.ErrPermissionDenied)
	var unchanged bool
	requireNoError(t, x.f.db.QueryRow(`SELECT version=1 AND name_key='Latent content' FROM public.roles WHERE id=$1`, x.targetRole).Scan(&unchanged))
	if !unchanged {
		t.Fatal("waiting editor retained revoked authority")
	}
}

func TestAuthRolesEditFirstIntegration(t *testing.T) {
	x := rolesManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lock := holdInstallationWriteMutex(t, x.f.db)
	edited, revoked := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := x.f.service.EditRole(ctx, authReq(x.actor), x.targetRole, 1, rolesInput("Editor committed", rolesContentGrant()))
		edited <- err
	}()
	waitForInstallationWriteWaiters(t, x.f.db, 1)
	go func() {
		_, err := x.f.db.ExecContext(ctx, `DELETE FROM public.role_grants WHERE role_id=$1 AND operation='roles.edit'`, x.managerRole)
		revoked <- err
	}()
	waitForInstallationWriteWaiters(t, x.f.db, 2)
	requireNoError(t, lock.Commit())
	requireNoError(t, <-edited)
	requireNoError(t, <-revoked)
	var committed bool
	requireNoError(t, x.f.db.QueryRow(`SELECT version=2 AND name_key='Editor committed' FROM public.roles WHERE id=$1`, x.targetRole).Scan(&committed))
	if !committed {
		t.Fatal("editor preceding revoke did not commit atomically")
	}
	_, err := x.f.service.EditRole(ctx, authReq(x.actor), x.targetRole, 2, rolesInput("After revoke", rolesContentGrant()))
	onboardingError(t, err, cms.ErrPermissionDenied)
}
