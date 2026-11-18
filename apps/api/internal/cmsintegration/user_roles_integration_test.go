package cmsintegration

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

func createMembershipTestRole(t *testing.T, f authFixtureState, name, state string) string {
	t.Helper()
	var id string
	requireNoError(t, f.db.QueryRow(`INSERT INTO public.roles(id,name_key,state)
 VALUES(gen_random_uuid(),$1,$2) RETURNING id::text`, name, state).Scan(&id))
	return id
}

func assertMembershipState(t *testing.T, f authFixtureState, user cms.User, version int64, roles []string) {
	t.Helper()
	var gotVersion int64
	var gotState, storedRoles string
	requireNoError(t, f.db.QueryRow(`SELECT state,version,COALESCE(
 (SELECT json_agg(role_id::text ORDER BY role_id) FROM public.user_roles WHERE user_id=u.id),'[]'::json
 )::text FROM public.users u WHERE id=$1`, user.ID).Scan(&gotState, &gotVersion, &storedRoles))
	var gotRoles []string
	requireNoError(t, json.Unmarshal([]byte(storedRoles), &gotRoles))
	expected := slices.Clone(roles)
	slices.Sort(expected)
	if gotState != user.State || gotVersion != version || !slices.Equal(gotRoles, expected) {
		t.Fatalf("stored account = %s v%d with %v, expected %s v%d with %v", gotState, gotVersion, gotRoles, user.State, version, expected)
	}
}

func TestAuthUserRolesLimitsAndLastAdminIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx := context.Background()
	admin := authFull(t, f)
	adminUser, err := f.service.GetUser(ctx, authReq(admin), f.user)
	requireNoError(t, err)
	adminRole := bootstrapAdminRoleID(t, f)
	_, err = f.service.ReplaceUserRoles(ctx, authReq(admin), adminUser.ID, adminUser.Version, []string{})
	onboardingError(t, err, cms.ErrLastAdminRequired)
	assertMembershipState(t, f, adminUser, adminUser.Version, []string{adminRole})

	recipient := lifecycleEnrolled(t, f, admin, "membership-limit@example.test")
	const caseSensitiveRole = "e0000000-0000-4000-8000-000000000001"
	_, err = f.db.Exec(`INSERT INTO public.roles(id,name_key,state) VALUES($1,'UUID normalization test','disabled')`, caseSensitiveRole)
	requireNoError(t, err)
	rows, err := f.db.Query(`INSERT INTO public.roles(id,name_key,state)
 SELECT gen_random_uuid(),'Membership test policy ' || n,'disabled' FROM generate_series(1,16) n RETURNING id::text`)
	requireNoError(t, err)
	defer func() { _ = rows.Close() }()
	roles := []string{caseSensitiveRole}
	for rows.Next() {
		var id string
		requireNoError(t, rows.Scan(&id))
		roles = append(roles, id)
	}
	requireNoError(t, rows.Err())
	_, err = f.service.ReplaceUserRoles(ctx, authReq(admin), recipient.user.ID, recipient.user.Version, roles)
	onboardingError(t, err, cms.ErrValidation)
	_, err = f.service.ReplaceUserRoles(ctx, authReq(admin), recipient.user.ID, recipient.user.Version, []string{caseSensitiveRole, strings.ToUpper(caseSensitiveRole)})
	onboardingError(t, err, cms.ErrValidation)
	expireSessionStepUp(t, f.db, admin.Token)
	_, err = f.service.ReplaceUserRoles(ctx, authReq(admin), recipient.user.ID, recipient.user.Version, roles[:1])
	onboardingError(t, err, cms.ErrStepUpRequired)
	assertMembershipState(t, f, recipient.user, recipient.user.Version, nil)
	authRefreshStep(t, f.db, admin.Token)

	roles = roles[:16]
	got, err := f.service.ReplaceUserRoles(ctx, authReq(admin), recipient.user.ID, recipient.user.Version, roles)
	requireNoError(t, err)
	if got.ID != recipient.user.ID || got.State != lifecycleHTTPDisabledState || got.Version != recipient.user.Version+1 {
		t.Fatal("membership replacement mutated disabled account state or returned the wrong revision")
	}
	assertMembershipState(t, f, recipient.user, got.Version, roles)
}

func TestAuthUserRolesEnvelopeAndSelfGrantIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	ctx := context.Background()
	_, err := x.f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,can_delegate) VALUES(gen_random_uuid(),$1,'roles.assign',true)`, x.managerRole)
	requireNoError(t, err)
	_, err = x.f.db.Exec(`UPDATE public.role_grants SET can_delegate=true WHERE role_id=$1 AND NOT can_delegate`, x.managerRole)
	requireNoError(t, err)
	adminRole := bootstrapAdminRoleID(t, x.f)
	_, err = x.f.service.ReplaceUserRoles(ctx, authReq(x.actor), x.f.user, 999, []string{})
	onboardingError(t, err, cms.ErrNotFound)
	_, err = x.f.service.ReplaceUserRoles(ctx, authReq(x.actor), "00000000-0000-4000-8000-000000000098", 999, []string{})
	onboardingError(t, err, cms.ErrNotFound)
	_, err = x.f.service.ReplaceUserRoles(ctx, authReq(x.actor), x.target.ID, x.target.Version, []string{adminRole})
	onboardingError(t, err, cms.ErrNotFound)
	assertMembershipState(t, x.f, x.target, x.target.Version, []string{x.targetRole})

	// The manager may delegate the assigned read grant without owning data access.
	_, err = x.f.db.Exec(`UPDATE public.roles SET state='active' WHERE id=$1`, x.targetRole)
	requireNoError(t, err)
	_, err = x.f.service.ReplaceUserRoles(ctx, authReq(x.actor), x.manager.ID, x.manager.Version, []string{x.managerRole, x.targetRole})
	onboardingError(t, err, cms.ErrPermissionDenied)
	assertMembershipState(t, x.f, x.manager, x.manager.Version, []string{x.managerRole})

	// An archived/disabled membership must not hide an out-of-envelope field.
	_, err = x.f.db.Exec(`UPDATE public.roles SET state='disabled' WHERE id=$1`, x.targetRole)
	requireNoError(t, err)
	_, err = x.f.db.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT id,type_id,$2 FROM public.role_grants WHERE role_id=$1`, x.targetRole, lifecycleOtherField)
	requireNoError(t, err)
	_, err = x.f.service.ReplaceUserRoles(ctx, authReq(x.actor), x.target.ID, 999, []string{})
	onboardingError(t, err, cms.ErrNotFound)
	assertMembershipState(t, x.f, x.target, x.target.Version, []string{x.targetRole})
}

func TestAuthUserRolesAuditRollbackIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	ctx := context.Background()
	admin := authFull(t, f.state)
	recipient := lifecycleEnrolled(t, f.state, admin, "membership-audit@example.test")
	user, err := f.state.service.ActivateUser(ctx, authReq(admin), recipient.user.ID, recipient.user.Version)
	requireNoError(t, err)
	oldRole := createAssignedTestRole(t, f.state, "Current recipient role", lifecycleHTTPActiveState, user.ID)
	newRole := createMembershipTestRole(t, f.state, "New recipient role", lifecycleHTTPActiveState)
	_, err = f.state.db.Exec(`CREATE FUNCTION public.fail_membership_audit() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.operation='users.roles_replaced' AND NEW.result='success' THEN
   RAISE EXCEPTION 'unavailable membership audit';
  END IF;
  RETURN NEW;
 END $$;
CREATE TRIGGER fail_membership_audit BEFORE INSERT ON public.security_audit
FOR EACH ROW EXECUTE FUNCTION public.fail_membership_audit()`)
	requireNoError(t, err)
	_, err = f.state.service.ReplaceUserRoles(ctx, authReq(admin), user.ID, user.Version, []string{newRole})
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	assertMembershipState(t, f.state, user, user.Version, []string{oldRole})
}

func TestAuthUserRolesRevokeFirstIntegration(t *testing.T) {
	x := lifecycleAccess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := x.f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.assign')`, x.managerRole)
	requireNoError(t, err)
	revoker := newRestrictedAuthService(t, x.f)
	lock := holdInstallationWriteMutex(t, x.f.db)
	revoked := make(chan error, 1)
	go func() {
		_, err := revoker.ReplaceUserRoles(ctx, authReq(x.admin), x.manager.ID, x.manager.Version, []string{})
		revoked <- err
	}()
	waitForInstallationWriteWaiters(t, x.f.db, 1)
	result := make(chan error, 1)
	go func() {
		_, err := x.f.service.ReplaceUserRoles(ctx, authReq(x.actor), x.target.ID, x.target.Version, []string{})
		result <- err
	}()
	waitForInstallationWriteWaiters(t, x.f.db, 2)
	requireNoError(t, lock.Commit())
	requireNoError(t, <-revoked)
	onboardingError(t, <-result, cms.ErrPermissionDenied)
	assertMembershipState(t, x.f, x.manager, x.manager.Version+1, nil)
	assertMembershipState(t, x.f, x.target, x.target.Version, []string{x.targetRole})
}
