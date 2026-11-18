package adminintegration

import (
	"context"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"testing"
)

const (
	lifecycleType       = "c0000000-0000-4000-8000-000000000001"
	lifecycleOtherType  = "c0000000-0000-4000-8000-000000000002"
	lifecycleField      = "c0000000-0000-4000-8000-000000000003"
	lifecycleOtherField = "c0000000-0000-4000-8000-000000000004"
)

type lifecycleAccessFixture struct {
	f                       authFixtureState
	admin, actor            identity.SessionTokens
	manager, target         identity.User
	managerRole, targetRole string
}

func lifecycleAccess(t *testing.T) lifecycleAccessFixture {
	t.Helper()
	f := newRestrictedAuthHTTPFixture(t).state
	full := authFull(t, f)
	manager := lifecycleEnrolled(t, f, full, "manager@example.test")
	var err error
	manager.user, err = f.service.ActivateUser(context.Background(), authReq(full), manager.user.ID, manager.user.Version)
	requireNoError(t, err)
	target := lifecycleEnrolled(t, f, full, lifecycleLogin)
	_, err = f.db.Exec(`WITH types AS (INSERT INTO public.content_types(id,key,route_namespace) VALUES($1,'lifecycle_content','articles'),($2,'lifecycle_other','events') RETURNING id) INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM types`, lifecycleType, lifecycleOtherType)
	requireNoError(t, err)
	_, err = f.db.Exec(`INSERT INTO public.content_fields(type_id,field_id,key) VALUES($1,$2,'title'),($1,$3,'hidden')`, lifecycleType, lifecycleField, lifecycleOtherField)
	requireNoError(t, err)
	roleID := createAssignedTestRole(t, f, "Lifecycle manager", "active", manager.user.ID)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) SELECT gen_random_uuid(),$1,op FROM unnest(ARRAY['users.read','users.activate','users.disable']) AS op`, roleID)
	requireNoError(t, err)
	var authority string
	requireNoError(t, f.db.QueryRow(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,delegate_operations,target_scope,object_ceiling) VALUES(gen_random_uuid(),$1,'content.permissions.delegate','installation',$2,ARRAY['content.items.read'],'type','assigned') RETURNING id::text`, roleID, lifecycleType).Scan(&authority))
	_, err = f.db.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id) VALUES($1,$2,$3)`, authority, lifecycleType, lifecycleField)
	requireNoError(t, err)
	targetRoleID := createAssignedTestRole(t, f, "Latent content", "archived", target.user.ID)
	var grant string
	requireNoError(t, f.db.QueryRow(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES(gen_random_uuid(),$1,'content.items.read','content',$2,'assigned') RETURNING id::text`, targetRoleID, lifecycleType).Scan(&grant))
	_, err = f.db.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id) VALUES($1,$2,$3)`, grant, lifecycleType, lifecycleField)
	requireNoError(t, err)
	return lifecycleAccessFixture{f, full, lifecycleLoginFull(t, f, manager), manager.user, target.user, roleID, targetRoleID}
}

func TestAuthLifecycleTypedAuthorityIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	ctx := context.Background()
	active, err := x.f.service.ActivateUser(ctx, authReq(x.actor), x.target.ID, x.target.Version)
	requireNoError(t, err)
	_, err = x.f.service.DisableUser(ctx, authReq(x.actor), active.ID, active.Version)
	requireNoError(t, err)
	var noData bool
	requireNoError(t, x.f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.role_grants WHERE role_id=$1 AND scope='content')`, x.managerRole).Scan(&noData))
	if !noData {
		t.Fatal("authority acquired implicit data grants")
	}
	requireNoError(t, database.CheckSchema(ctx, x.f.db))
}

func TestAuthLifecycleLatentEnvelopeIntegration(t *testing.T) {
	cases := []struct{ name, query string }{
		{"field", `INSERT INTO public.grant_fields(grant_id,type_id,field_id) SELECT id,type_id,'c0000000-0000-4000-8000-000000000004' FROM public.role_grants WHERE role_id=$1`},
		{"ceiling", `UPDATE public.role_grants SET object_scope='any' WHERE role_id=$1`},
		{"delegation flag", `UPDATE public.role_grants SET can_delegate=true WHERE role_id=$1`},
		{"security superior", `INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.edit')`},
		{"other type", `INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES(gen_random_uuid(),$1,'content.items.read','content','c0000000-0000-4000-8000-000000000002','assigned')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := lifecycleAccess(t)
			_, err := x.f.db.Exec(tc.query, x.targetRole)
			requireNoError(t, err)
			_, err = x.f.service.ActivateUser(context.Background(), authReq(x.actor), x.target.ID, 999)
			onboardingError(t, err, identity.ErrNotFound)
			var unchanged bool
			requireNoError(t, x.f.db.QueryRow(`SELECT state='disabled' AND version=$2 FROM public.users WHERE id=$1`, x.target.ID, x.target.Version).Scan(&unchanged))
			if !unchanged {
				t.Fatal("latent superior target changed")
			}
		})
	}
}

func TestAuthLifecycleAuthorityDelegationIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	_, err := x.f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,type_id,delegate_operations,target_scope,object_ceiling) VALUES(gen_random_uuid(),$1,'content.permissions.delegate',$2,ARRAY['content.items.read'],'type','assigned')`, x.targetRole, lifecycleType)
	requireNoError(t, err)
	_, err = x.f.service.ActivateUser(context.Background(), authReq(x.actor), x.target.ID, x.target.Version)
	onboardingError(t, err, identity.ErrNotFound)
	_, err = x.f.db.Exec(`UPDATE public.role_grants SET can_delegate=true WHERE role_id=$1 AND operation='content.permissions.delegate'`, x.managerRole)
	requireNoError(t, err)
	_, err = x.f.db.Exec(`UPDATE public.role_grants SET delegate_operations=ARRAY['content.items.read','content.items.update'] WHERE role_id=$1 AND operation='content.permissions.delegate'`, x.targetRole)
	requireNoError(t, err)
	_, err = x.f.service.ActivateUser(context.Background(), authReq(x.actor), x.target.ID, x.target.Version)
	onboardingError(t, err, identity.ErrNotFound)
}

func TestAuthLifecycleAuthoritySchemaIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	for _, query := range []string{
		`INSERT INTO public.role_grants(id,role_id,operation,delegate_operations,target_scope,object_ceiling) VALUES(gen_random_uuid(),$1,'content.permissions.delegate',ARRAY['content.items.update'],'type','any')`,
		`INSERT INTO public.role_grants(id,role_id,operation,type_id,delegate_operations,target_scope,object_ceiling) VALUES(gen_random_uuid(),$1,'content.permissions.delegate','c0000000-0000-4000-8000-000000000001',ARRAY['content.items.update'],'installation','any')`,
		`INSERT INTO public.role_grants(id,role_id,operation,type_id) VALUES(gen_random_uuid(),$1,'roles.read','c0000000-0000-4000-8000-000000000001')`,
	} {
		_, err := x.f.db.Exec(query, x.managerRole)
		assertSQLState(t, err, "23514")
	}
}
