package database

import (
	"context"
	"testing"

	"stepolonia/apps/api/internal/cms"
)

const (
	rolesContentScope  = "content"
	rolesArchivedState = "archived"
)

func rolesInput(name string, grants ...cms.RoleGrant) cms.RoleInput {
	if grants == nil {
		grants = []cms.RoleGrant{}
	}
	return cms.RoleInput{Name: name, Grants: grants}
}

func rolesContentGrant() cms.RoleGrant {
	return cms.RoleGrant{Operation: bootstrapOpRead, Scope: rolesContentScope, TypeID: lifecycleType, ObjectScope: "assigned", FieldIDs: []string{lifecycleField}}
}

func rolesManager(t *testing.T) lifecycleAccessFixture {
	t.Helper()
	x := lifecycleAccess(t)
	_, err := x.f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) SELECT gen_random_uuid(),$1,op FROM unnest(ARRAY['roles.read','roles.create','roles.edit']) op`, x.managerRole)
	requireNoError(t, err)
	return x
}

func TestAuthRolesCreateEditIntegration(t *testing.T) {
	x := rolesManager(t)
	ctx := context.Background()
	created, err := x.f.service.CreateRole(ctx, authReq(x.actor), rolesInput("New delegated role", rolesContentGrant()))
	requireNoError(t, err)
	if created.State != lifecycleDisabledState || created.Version != 1 || len(created.Grants) != 1 {
		t.Fatalf("invalid created role: %+v", created)
	}
	var noMembership bool
	requireNoError(t, x.f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.user_roles WHERE role_id=$1)`, created.ID).Scan(&noMembership))
	if !noMembership {
		t.Fatal("creation assigned recipients")
	}
	// Active and inactive recipients retain all their latent grants in checks.
	edited, err := x.f.service.EditRole(ctx, authReq(x.actor), x.targetRole, 1, rolesInput("Changed latent role", rolesContentGrant()))
	requireNoError(t, err)
	if edited.State != rolesArchivedState || edited.Version != 2 || edited.Name != "Changed latent role" {
		t.Fatal("editor changed state or lost version")
	}
	_, err = x.f.service.EditRole(ctx, authReq(x.actor), edited.ID, 1, rolesInput("Stale role", rolesContentGrant()))
	onboardingError(t, err, cms.ErrVersionConflict)
	_, err = x.f.service.EditRole(ctx, authReq(x.actor), edited.ID, 0, rolesInput("Invalid role", rolesContentGrant()))
	onboardingError(t, err, cms.ErrValidation)
	got, err := x.f.service.GetRole(ctx, authReq(x.actor), edited.ID)
	requireNoError(t, err)
	if got.Name != edited.Name || got.Version != 2 {
		t.Fatal("failed edit mutated role")
	}
	requireNoError(t, CheckSchema(ctx, x.f.db))
}

func TestAuthRolesInputValidationIntegration(t *testing.T) {
	x := rolesManager(t)
	ctx := context.Background()
	cases := []cms.RoleInput{
		rolesInput("", rolesContentGrant()),
		rolesInput("unknown", cms.RoleGrant{Operation: "unknown.operation", Scope: "installation"}),
		rolesInput("duplicate", rolesContentGrant(), rolesContentGrant()),
		rolesInput("wrong field", cms.RoleGrant{Operation: bootstrapOpRead, Scope: rolesContentScope, TypeID: lifecycleOtherType, ObjectScope: "assigned", FieldIDs: []string{lifecycleField}}),
		rolesInput("unknown type", cms.RoleGrant{Operation: bootstrapOpRead, Scope: rolesContentScope, TypeID: "00000000-0000-4000-8000-000000000099", ObjectScope: "any"}),
	}
	for _, input := range cases {
		_, err := x.f.service.CreateRole(ctx, authReq(x.actor), input)
		onboardingError(t, err, cms.ErrValidation)
	}
	var count int
	requireNoError(t, x.f.db.QueryRow(`SELECT count(*) FROM public.roles WHERE name_key IN ('unknown','duplicate','wrong field','unknown type')`).Scan(&count))
	if count != 0 {
		t.Fatal("invalid grant set partially created")
	}
}

func TestAuthRolesEnvelopeIntegration(t *testing.T) {
	cases := []struct{ name, query string }{
		{"superior grant", `INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.edit')`},
		{"latent superior recipient", `WITH r AS (INSERT INTO public.roles(id,name_key,state) VALUES(gen_random_uuid(),'Other latent superior','archived') RETURNING id),g AS (INSERT INTO public.role_grants(id,role_id,operation) SELECT gen_random_uuid(),id,'roles.edit' FROM r) INSERT INTO public.user_roles(user_id,role_id) SELECT ur.user_id,r.id FROM r CROSS JOIN public.user_roles ur WHERE ur.role_id=$1`},
		{"any ceiling", `UPDATE public.role_grants SET object_scope='any' WHERE role_id=$1`},
		{"delegation growth", `UPDATE public.role_grants SET can_delegate=true WHERE role_id=$1`},
		{"hidden field", `INSERT INTO public.grant_fields(grant_id,type_id,field_id) SELECT id,type_id,'c0000000-0000-4000-8000-000000000004' FROM public.role_grants WHERE role_id=$1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := rolesManager(t)
			_, err := x.f.db.Exec(tc.query, x.targetRole)
			requireNoError(t, err)
			_, err = x.f.service.EditRole(context.Background(), authReq(x.actor), x.targetRole, 999, rolesInput("Forbidden role"))
			onboardingError(t, err, cms.ErrNotFound)
		})
	}
}

func TestAuthRolesOwnGrowthIntegration(t *testing.T) {
	for _, state := range []string{lifecycleHTTPActiveState, lifecycleDisabledState, rolesArchivedState} {
		t.Run(state, func(t *testing.T) {
			f := qaOnboardingFixture(t).state
			full := authFull(t, f)
			_, err := f.db.Exec(`WITH t AS(INSERT INTO public.content_types(id,key,route_namespace) VALUES($1,'role_new_type','role-new-type') RETURNING id) INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM t`, lifecycleType)
			requireNoError(t, err)
			role := lifecycleRole(t, f, "Shared self role", state, f.user)
			input := rolesInput("Shared self growth", cms.RoleGrant{Operation: bootstrapOpRead, Scope: rolesContentScope, TypeID: lifecycleType, ObjectScope: "any"})
			_, err = f.service.EditRole(context.Background(), authReq(full), role, 1, input)
			onboardingError(t, err, cms.ErrPermissionDenied)
			var unchanged bool
			requireNoError(t, f.db.QueryRow(`SELECT version=1 AND name_key='Shared self role' AND NOT EXISTS(SELECT 1 FROM public.role_grants WHERE role_id=$1) FROM public.roles WHERE id=$1`, role).Scan(&unchanged))
			if !unchanged {
				t.Fatal("self growth committed")
			}
		})
	}
}

func TestAuthRolesLastAdminAndRollbackIntegration(t *testing.T) {
	f := qaOnboardingFixture(t).state
	full := authFull(t, f)
	var role string
	requireNoError(t, f.db.QueryRow(`SELECT role_id::text FROM public.user_roles WHERE user_id=$1`, f.user).Scan(&role))
	admin, err := f.service.GetRole(context.Background(), authReq(full), role)
	requireNoError(t, err)
	_, err = f.service.EditRole(context.Background(), authReq(full), role, admin.Version, rolesInput("Remove last admin"))
	onboardingError(t, err, cms.ErrLastAdminRequired)
	_, err = f.db.Exec(`CREATE FUNCTION public.roles_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation IN ('roles.created','roles.edited') THEN RAISE EXCEPTION 'test roles audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER roles_audit_failure BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.roles_audit_failure()`)
	requireNoError(t, err)
	_, err = f.service.EditRole(context.Background(), authReq(full), role, admin.Version, rolesInput("Audit rollback", admin.Grants...))
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	_, err = f.service.CreateRole(context.Background(), authReq(full), rolesInput("Audit failed creation"))
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	var unchanged bool
	requireNoError(t, f.db.QueryRow(`SELECT version=$2 AND name_key=$3 AND NOT EXISTS(SELECT 1 FROM public.roles WHERE name_key='Audit failed creation') FROM public.roles WHERE id=$1`, role, admin.Version, admin.Name).Scan(&unchanged))
	if !unchanged {
		t.Fatal("audit outage committed partial role state")
	}
}
