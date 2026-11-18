package cmsintegration

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

const (
	roleDeletePermission = "roles.delete"
	roleEditPermission   = "roles.edit"
)

// Give the manager authority to retire roles as well as create and edit them.
func roleDeletionManager(t *testing.T) lifecycleAccessFixture {
	t.Helper()
	x := rolesManager(t)
	addTestInstallationGrant(t, x.f, x.managerRole, roleDeletePermission)
	return x
}

func removeTestMembership(t *testing.T, f authFixtureState, userID, roleID string) {
	t.Helper()
	_, err := f.db.Exec(`DELETE FROM public.user_roles WHERE user_id=$1 AND role_id=$2`, userID, roleID)
	requireNoError(t, err)
}

func TestAuthRoleCopyExplicitGrantsIntegration(t *testing.T) {
	x := rolesManager(t)
	ctx, req := context.Background(), authReq(x.actor)
	source, err := x.f.service.GetRole(ctx, req, x.targetRole)
	requireNoError(t, err)
	copied, err := x.f.service.CopyRole(ctx, req, source.ID, source.Version, "Article reviewers")
	requireNoError(t, err)
	if copied.ID == source.ID || copied.Name != "Article reviewers" || copied.Version != 1 || copied.State != lifecycleDisabledState ||
		len(copied.Grants) != 1 || !reflect.DeepEqual(copied.Grants, source.Grants) || !reflect.DeepEqual(copied.Grants[0].FieldIDs, []string{lifecycleField}) {
		t.Fatalf("unexpected copied policy: %+v", copied)
	}
	var independent bool
	requireNoError(t, x.f.db.QueryRow(`SELECT
		EXISTS(SELECT 1 FROM public.user_roles WHERE role_id=$1 AND user_id=$3) AND
		NOT EXISTS(SELECT 1 FROM public.user_roles WHERE role_id=$2) AND
		EXISTS(SELECT 1 FROM public.grant_fields f JOIN public.role_grants g ON g.id=f.grant_id WHERE g.role_id=$2 AND f.field_id=$4) AND
		(SELECT count(*)=1 FROM public.security_audit WHERE operation='roles.copied' AND result='success' AND target_id=$2)`, source.ID, copied.ID, x.target.ID, lifecycleField).Scan(&independent))
	if !independent {
		t.Fatal("copy lost its audit, field grant or membership isolation")
	}
	_, err = x.f.service.CopyRole(ctx, req, source.ID, source.Version, copied.Name)
	onboardingError(t, err, cms.ErrVersionConflict)
	_, err = x.f.service.CopyRole(ctx, req, source.ID, source.Version+1, "Stale copy")
	onboardingError(t, err, cms.ErrVersionConflict)
	after, err := x.f.service.GetRole(ctx, req, source.ID)
	requireNoError(t, err)
	if !reflect.DeepEqual(source, after) {
		t.Fatal("copy mutated its source")
	}
}

func TestAuthRoleDeleteDependenciesIntegration(t *testing.T) {
	x := roleDeletionManager(t)
	ctx, req := context.Background(), authReq(x.actor)
	onboardingError(t, x.f.service.DeleteRole(ctx, req, x.targetRole, 1), cms.ErrRoleInUse)
	var intact bool
	requireNoError(t, x.f.db.QueryRow(`SELECT
		EXISTS(SELECT 1 FROM public.user_roles WHERE role_id=$1 AND user_id=$2) AND
		EXISTS(SELECT 1 FROM public.grant_fields f JOIN public.role_grants g ON g.id=f.grant_id WHERE g.role_id=$1 AND f.field_id=$3)`, x.targetRole, x.target.ID, lifecycleField).Scan(&intact))
	if !intact {
		t.Fatal("rejected deletion removed a dependency")
	}
	removeTestMembership(t, x.f, x.target.ID, x.targetRole)
	onboardingError(t, x.f.service.DeleteRole(ctx, req, x.targetRole, 1), cms.ErrRoleInUse)
	role, err := x.f.service.EditRole(ctx, req, x.targetRole, 1, rolesInput("Retiring reviewers"))
	requireNoError(t, err)
	lock := holdInstallationWriteMutex(t, x.f.db)
	_, err = lock.ExecContext(ctx, `INSERT INTO public.user_roles(role_id,user_id) VALUES($1,$2)`, role.ID, x.target.ID)
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() { result <- x.f.service.DeleteRole(ctx, req, role.ID, role.Version) }()
	waitForInstallationWriteWaiters(t, x.f.db, 1)
	requireNoError(t, lock.Commit())
	onboardingError(t, <-result, cms.ErrRoleInUse)
	removeTestMembership(t, x.f, x.target.ID, role.ID)
	onboardingError(t, x.f.service.DeleteRole(ctx, req, role.ID, 1), cms.ErrVersionConflict)
	requireNoError(t, x.f.service.DeleteRole(ctx, req, role.ID, role.Version))
	onboardingError(t, x.f.service.DeleteRole(ctx, req, role.ID, role.Version), cms.ErrNotFound)
	var removed bool
	requireNoError(t, x.f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.roles WHERE id=$1) AND
		(SELECT count(*)=2 AND count(DISTINCT operation)=2 FROM public.security_audit
		WHERE target_id=$1 AND operation IN ('roles.edited','roles.deleted') AND result='success')`, role.ID).Scan(&removed))
	if !removed {
		t.Fatal("hard delete lost role history or retained the role")
	}
}

func TestAuthRoleCopyDeleteHiddenTargetIntegration(t *testing.T) {
	x := roleDeletionManager(t)
	ctx, req := context.Background(), authReq(x.actor)
	// The actor can manage the source grant but cannot delegate a recipient's other latent grants.
	superior := createAssignedTestRole(t, x.f, "Restricted recipient authority", rolesArchivedState, x.target.ID)
	addTestInstallationGrant(t, x.f, superior, roleEditPermission)
	_, err := x.f.service.CopyRole(ctx, req, x.targetRole, 999, "Hidden recipient")
	onboardingError(t, err, cms.ErrNotFound)
	onboardingError(t, x.f.service.DeleteRole(ctx, req, x.targetRole, 999), cms.ErrNotFound)
	removeTestMembership(t, x.f, x.target.ID, superior)
	_, err = x.f.db.Exec(`UPDATE public.role_grants SET object_scope=$2 WHERE role_id=$1 AND scope='content'`, x.targetRole, bootstrapAny)
	requireNoError(t, err)
	_, err = x.f.service.CopyRole(ctx, req, x.targetRole, 1, "Above delegation ceiling")
	onboardingError(t, err, cms.ErrNotFound)
	onboardingError(t, x.f.service.DeleteRole(ctx, req, x.targetRole, 1), cms.ErrNotFound)
}

func TestAuthRoleCopyDeleteRevokedPermissionIntegration(t *testing.T) {
	for _, operation := range []string{"roles.create", roleEditPermission, roleDeletePermission} {
		t.Run(operation, func(t *testing.T) {
			x := roleDeletionManager(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var target string
			requireNoError(t, x.f.db.QueryRow(`INSERT INTO public.roles(id,name_key,state) VALUES(gen_random_uuid(),'Retiring policy','archived') RETURNING id`).Scan(&target))
			lock := holdInstallationWriteMutex(t, x.f.db)
			_, err := lock.ExecContext(ctx, `DELETE FROM public.role_grants WHERE role_id=$1 AND operation=$2`, x.managerRole, operation)
			requireNoError(t, err)
			result := make(chan error, 1)
			go func() {
				if operation == roleDeletePermission {
					result <- x.f.service.DeleteRole(ctx, authReq(x.actor), target, 1)
					return
				}
				_, err := x.f.service.CopyRole(ctx, authReq(x.actor), target, 1, "Waiting clone")
				result <- err
			}()
			waitForInstallationWriteWaiters(t, x.f.db, 1)
			requireNoError(t, lock.Commit())
			onboardingError(t, <-result, cms.ErrPermissionDenied)
			var unchanged bool
			requireNoError(t, x.f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.roles WHERE id=$1)
				AND NOT EXISTS(SELECT 1 FROM public.roles WHERE name_key='Waiting clone')`, target).Scan(&unchanged))
			if !unchanged {
				t.Fatal("revoked authority committed a role mutation")
			}
		})
	}
}

func TestAuthRoleCopyDeleteAuditRollbackIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx, req := context.Background(), authReq(authFull(t, f))
	withGrants, err := f.service.CreateRole(ctx, req, rolesInput("Copy source", userReaderGrant()))
	requireNoError(t, err)
	empty, err := f.service.CreateRole(ctx, req, rolesInput("Delete target"))
	requireNoError(t, err)
	archived, err := f.service.ArchiveRole(ctx, req, empty.ID, empty.Version)
	requireNoError(t, err)
	_, err = f.db.Exec(`CREATE FUNCTION public.fail_role_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.operation IN ('roles.copied','roles.deleted') THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fail_role_audit BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.fail_role_audit()`)
	requireNoError(t, err)
	_, err = f.service.CopyRole(ctx, req, withGrants.ID, withGrants.Version, "Uncommitted copy")
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	onboardingError(t, f.service.DeleteRole(ctx, req, archived.ID, archived.Version), cms.ErrSecurityUnavailable)
	got, err := f.service.GetRole(ctx, req, archived.ID)
	requireNoError(t, err)
	var absent bool
	requireNoError(t, f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.roles WHERE name_key='Uncommitted copy')`).Scan(&absent))
	if !absent || !reflect.DeepEqual(got, archived) {
		t.Fatal("audit failure committed a role mutation")
	}
}

func TestAuthRoleCopyDeleteLastAdminIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	ctx, req := context.Background(), authReq(authFull(t, f))
	role, err := f.service.CreateRole(ctx, req, rolesInput("Archived policy"))
	requireNoError(t, err)
	role, err = f.service.ArchiveRole(ctx, req, role.ID, role.Version)
	requireNoError(t, err)
	// The administrator can still invoke both commands but cannot delegate audit.read to a successor.
	_, err = f.db.Exec(`UPDATE public.role_grants SET can_delegate=false WHERE role_id=$1 AND operation='audit.read'`, bootstrapAdminRoleID(t, f))
	requireNoError(t, err)
	_, err = f.service.CopyRole(ctx, req, role.ID, role.Version, "Incomplete successor")
	onboardingError(t, err, cms.ErrLastAdminRequired)
	onboardingError(t, f.service.DeleteRole(ctx, req, role.ID, role.Version), cms.ErrLastAdminRequired)
}

func TestAuthRoleCopyDeleteCompetingCommandsIntegration(t *testing.T) {
	for _, order := range []struct {
		name        string
		deleteFirst bool
	}{{"delete first", true}, {"copy first", false}} {
		t.Run(order.name, func(t *testing.T) {
			deleteFirst := order.deleteFirst
			f := newRestrictedAuthHTTPFixture(t).state
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req := authReq(authFull(t, f))
			role, err := f.service.CreateRole(ctx, req, rolesInput("Concurrent commands"))
			requireNoError(t, err)
			role, err = f.service.ArchiveRole(ctx, req, role.ID, role.Version)
			requireNoError(t, err)
			lock := holdInstallationWriteMutex(t, f.db)
			remove := func() error { return f.service.DeleteRole(ctx, req, role.ID, role.Version) }
			copyRole := func() error {
				_, copyErr := f.service.CopyRole(ctx, req, role.ID, role.Version, "Concurrent copy")
				return copyErr
			}
			first, second := copyRole, remove
			if deleteFirst {
				first, second = remove, copyRole
			}
			a, b := make(chan error, 1), make(chan error, 1)
			go func() { a <- first() }()
			waitForInstallationWriteWaiters(t, f.db, 1)
			go func() { b <- second() }()
			waitForInstallationWriteWaiters(t, f.db, 2)
			requireNoError(t, lock.Commit())
			requireNoError(t, <-a)
			if deleteFirst {
				onboardingError(t, <-b, cms.ErrNotFound)
			} else {
				requireNoError(t, <-b)
			}
			var copied, removed bool
			requireNoError(t, f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.roles WHERE name_key='Concurrent copy'), NOT EXISTS(SELECT 1 FROM public.roles WHERE id=$1)`, role.ID).Scan(&copied, &removed))
			if copied == deleteFirst || !removed {
				t.Fatalf("delete first=%t, copy exists=%t, source removed=%t", deleteFirst, copied, removed)
			}
		})
	}
}

func TestAuthRoleCopyDeleteFreshMFAIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	admin := authFull(t, f.state)
	req := authReq(admin)
	ctx := context.Background()
	role, err := f.state.service.CreateRole(ctx, req, rolesInput("Fresh MFA", userReaderGrant()))
	requireNoError(t, err)
	role, err = f.state.service.ArchiveRole(ctx, req, role.ID, role.Version)
	requireNoError(t, err)
	expireSessionStepUp(t, f.state.db, admin.Token)
	path := rolesHTTPPath + "/" + role.ID
	out, _ := f.roleHTTP(t, http.MethodPost, path+"/copy", `{"name":"Expired MFA","expected_version":2}`, admin, 403)
	if out["code"] != rolesStepUpCode {
		t.Fatal("copy accepted stale MFA or returned the wrong reason")
	}
	out, _ = f.roleHTTP(t, http.MethodDelete, path, onboardingVersionJSON(t, role.Version), admin, 403)
	if out["code"] != rolesStepUpCode {
		t.Fatal("delete accepted stale MFA or returned the wrong reason")
	}
	authRefreshStep(t, f.state.db, admin.Token)
	out, _ = f.roleHTTP(t, http.MethodDelete, path, onboardingVersionJSON(t, role.Version), admin, 409)
	if out["code"] != "role_in_use" {
		t.Fatalf("archived role with grants: %v", out["code"])
	}
}
