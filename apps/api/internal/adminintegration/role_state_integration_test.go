package adminintegration

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

func TestAuthRoleStateHTTPIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	admin := authFull(t, f.state)
	created, err := f.state.service.CreateRole(context.Background(), authReq(admin), rolesInput("State policy", userReaderGrant()))
	requireNoError(t, err)
	path := rolesHTTPPath + "/" + created.ID
	want, _ := f.roleHTTP(t, http.MethodGet, path, "", admin, 200)
	for _, body := range []string{`{}`, `{"expected_version":0}`, `{"expected_version":1,"state":"active"}`, `{"expected_version":1,"actor_id":"x"}`, `{"expected_version":1,"grants":[]}`} {
		f.roleHTTP(t, http.MethodPost, path+"/enable", body, admin, 400)
	}
	expireSessionStepUp(t, f.state.db, admin.Token)
	problem, _ := f.roleHTTP(t, http.MethodPost, path+"/enable", `{"expected_version":1}`, admin, 403)
	if problem["code"] != stepUpRequiredProblem {
		t.Fatalf("expired step-up: %v", problem["code"])
	}
	authRefreshStep(t, f.state.db, admin.Token)
	for i, tc := range []struct{ action, state, event string }{
		{"enable", lifecycleHTTPActiveState, "roles.enabled"}, {"disable", lifecycleHTTPDisabledState, "roles.disabled"},
		{"archive", rolesArchivedState, "roles.archived"}, {"restore", lifecycleHTTPDisabledState, "roles.restored"},
	} {
		version := int64(i + 1)
		route := path + "/" + tc.action
		got, requestID := f.roleHTTP(t, http.MethodPost, route, onboardingVersionJSON(t, version), admin, 200)
		want["state"], want["version"] = tc.state, float64(version+1)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s response = %v, want %v", tc.action, got, want)
		}
		var audited bool
		requireNoError(t, f.state.db.QueryRow(`SELECT count(*)=1 FROM public.security_audit WHERE request_id=$1 AND target_id=$2 AND operation=$3 AND result='success'`, requestID, created.ID, tc.event).Scan(&audited))
		if !audited {
			t.Fatalf("missing %s audit", tc.event)
		}
		problem, _ := f.roleHTTP(t, http.MethodPost, route, onboardingVersionJSON(t, version+1), admin, 409)
		if problem["code"] != "invalid_transition" {
			t.Fatalf("repeated %s: %v", tc.action, problem["code"])
		}
	}
	problem, _ = f.roleHTTP(t, http.MethodPost, path+"/enable", onboardingVersionJSON(t, 1), admin, 409)
	if problem["code"] != "version_conflict" {
		t.Fatalf("stale version: %v", problem["code"])
	}
}

func TestAuthRoleStateRecipientEnvelopeIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	ctx := context.Background()
	_, err := x.f.service.RestoreRole(ctx, authReq(x.actor), x.targetRole, 1)
	onboardingError(t, err, identity.ErrPermissionDenied)
	_, err = x.f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.restore'),(gen_random_uuid(),$1,'roles.enable')`, x.managerRole)
	requireNoError(t, err)
	restored, err := x.f.service.RestoreRole(ctx, authReq(x.actor), x.targetRole, 1)
	requireNoError(t, err)
	// The disabled recipient has an archived second role above the manager's authority.
	superior := createAssignedTestRole(t, x.f, "Recipient superior", rolesArchivedState, x.target.ID)
	addTestInstallationGrant(t, x.f, superior, roleEditPermission)
	_, err = x.f.service.EnableRole(ctx, authReq(x.actor), x.targetRole, 999)
	onboardingError(t, err, identity.ErrNotFound)
	_, err = x.f.service.RestoreRole(ctx, authReq(x.actor), x.targetRole, restored.Version)
	onboardingError(t, err, identity.ErrNotFound)
	_, err = x.f.db.Exec(`DELETE FROM public.user_roles WHERE role_id=$1`, superior)
	requireNoError(t, err)
	_, err = x.f.service.EnableRole(ctx, authReq(x.actor), x.targetRole, restored.Version)
	requireNoError(t, err)
}

func TestAuthRoleStateDenialBudgetIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	for i := range 6 {
		_, err := x.f.service.RestoreRole(context.Background(), authReq(x.actor), x.targetRole, 1)
		want := identity.ErrPermissionDenied
		if i == 5 {
			want = identity.ErrRateLimited
		}
		onboardingError(t, err, want)
	}
	var bounded bool
	requireNoError(t, x.f.db.QueryRow(`SELECT count(*)=5 AND bool_and(target_id IS NULL AND reason='invalid_credentials' AND request_id IS NOT NULL) FROM public.security_audit WHERE operation='roles.restored' AND result='denied'`).Scan(&bounded))
	if !bounded {
		t.Fatal("denied restores exceeded their audit budget or exposed a target")
	}
}

func TestAuthRoleStateOwnGrowthIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	full := authFull(t, f)
	other := lifecycleEnrolled(t, f, full, lifecycleLogin)
	role := createAssignedTestRole(t, f, "Shared content", lifecycleDisabledState, other.user.ID)
	_, err := f.db.Exec(`INSERT INTO public.user_roles(user_id,role_id) VALUES($1,$2)`, f.user, role)
	requireNoError(t, err)
	_, err = f.db.Exec(`WITH created AS (INSERT INTO public.content_types(id,key,route_namespace) VALUES($1,'shared_type','shared-type') RETURNING id) INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM created`, lifecycleType)
	requireNoError(t, err)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES(gen_random_uuid(),$1,'content.items.read','content',$2,'any')`, role, lifecycleType)
	requireNoError(t, err)
	_, err = f.service.EnableRole(context.Background(), authReq(full), role, 1)
	onboardingError(t, err, identity.ErrPermissionDenied)
	var unchanged bool
	requireNoError(t, f.db.QueryRow(`SELECT state='disabled' AND version=1 AND (SELECT count(*)=2 FROM public.user_roles WHERE role_id=$1) FROM public.roles WHERE id=$1`, role).Scan(&unchanged))
	if !unchanged {
		t.Fatal("self access growth changed role or recipients")
	}
}

func TestAuthRoleStateLastAdminIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	role := bootstrapAdminRoleID(t, f)
	admin, err := f.service.GetRole(ctx, authReq(full), role)
	requireNoError(t, err)
	_, err = f.service.DisableRole(ctx, authReq(full), role, admin.Version)
	onboardingError(t, err, identity.ErrLastAdminRequired)
	next := lifecycleEnrolled(t, f, full, lifecycleLogin)
	_, err = f.service.ActivateUser(ctx, authReq(full), next.user.ID, next.user.Version)
	requireNoError(t, err)
	backup := createAssignedTestRole(t, f, "Successor", lifecycleHTTPActiveState, next.user.ID)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope,can_delegate,delegate_operations,target_scope,object_ceiling,may_delegate_content)
SELECT gen_random_uuid(),$2,operation,scope,type_id,object_scope,can_delegate,delegate_operations,target_scope,object_ceiling,may_delegate_content FROM public.role_grants WHERE role_id=$1 AND operation<>'audit.read'`, role, backup)
	requireNoError(t, err)
	_, err = f.service.DisableRole(ctx, authReq(full), role, admin.Version)
	onboardingError(t, err, identity.ErrLastAdminRequired)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,can_delegate) VALUES(gen_random_uuid(),$1,'audit.read',true)`, backup)
	requireNoError(t, err)
	_, err = f.service.DisableRole(ctx, authReq(full), role, admin.Version)
	requireNoError(t, err)
	_, err = f.service.GetRole(ctx, authReq(full), role)
	onboardingError(t, err, identity.ErrPermissionDenied)
	replacement := lifecycleLoginFull(t, f, next)
	_, err = f.service.GetRole(ctx, authReq(replacement), role)
	requireNoError(t, err)
}
