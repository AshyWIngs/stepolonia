package adminintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/testpostgres"
)

const (
	assigneeTypeID    = "a3000000-0000-4000-8000-000000000001"
	assigneeOtherType = "a3000000-0000-4000-8000-000000000002"
	assigneeItem      = "a3000000-0000-4000-8000-000000000003"
	assigneeOtherItem = "a3000000-0000-4000-8000-000000000004"
	assigneeMissingID = "a3000000-0000-4000-8000-000000000005"
	assigneeTypeKey   = "assignment_articles"
	assigneeOtherKey  = "assignment_events"
)

type assigneeFixture struct {
	http                authHTTPFixture
	admin, actor        identity.SessionTokens
	manager, recipient  lifecycleAccount
	managerRole, reader string
}

func newAssigneeFixture(t *testing.T) assigneeFixture {
	t.Helper()
	f := newRestrictedAuthHTTPFixture(t)
	ctx := context.Background()
	admin := authFull(t, f.state)
	activeAccount := func(login string) lifecycleAccount {
		account := lifecycleEnrolled(t, f.state, admin, login)
		user, err := f.state.service.ActivateUser(ctx, authReq(admin), account.user.ID, account.user.Version)
		requireNoError(t, err)
		account.user = user
		return account
	}
	manager := activeAccount("assigning-manager@example.test")
	recipient := activeAccount("assigned-reader@example.test")
	managerRole := createAssignedTestRole(t, f.state, "Assignment manager", lifecycleHTTPActiveState, manager.user.ID)
	reader := createAssignedTestRole(t, f.state, "Assignment reader", lifecycleHTTPActiveState, recipient.user.ID)
	_, err := f.state.db.Exec(`WITH types AS (
 INSERT INTO public.content_types(id,key,route_namespace) VALUES
 ($1,$2,'assignment-articles'),($3,$4,'assignment-events') RETURNING id
 ) INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM types`,
		assigneeTypeID, assigneeTypeKey, assigneeOtherType, assigneeOtherKey)
	requireNoError(t, err)
	_, err = f.state.db.Exec(`INSERT INTO public.content_items(id,type_id) VALUES ($1,$2),($3,$4)`,
		assigneeItem, assigneeTypeID, assigneeOtherItem, assigneeOtherType)
	requireNoError(t, err)
	_, err = f.state.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES
 (gen_random_uuid(),$1,'content.items.assign','content',$3,'any'),
 (gen_random_uuid(),$2,'content.items.read','content',$3,'assigned'),
 (gen_random_uuid(),$2,'content.items.read','content',$4,'assigned')`,
		managerRole, reader, assigneeTypeID, assigneeOtherType)
	requireNoError(t, err)
	_, err = f.state.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,type_id,delegate_operations,target_scope,object_ceiling)
 VALUES(gen_random_uuid(),$1,'content.permissions.delegate',$2,ARRAY['content.items.read'],'type','assigned')`,
		managerRole, assigneeTypeID)
	requireNoError(t, err)
	return assigneeFixture{f, admin, lifecycleLoginFull(t, f.state, manager), manager, recipient, managerRole, reader}
}

func (x assigneeFixture) replace(ctx context.Context, version int64, ids ...string) (identity.ContentAssignees, error) {
	if ids == nil {
		ids = []string{}
	}
	return x.http.state.service.ReplaceContentAssignees(ctx, authReq(x.actor), assigneeTypeKey, assigneeItem, version, ids)
}

func assertItemAssignees(t *testing.T, db *sql.DB, itemID string, version int64, ids ...string) {
	t.Helper()
	var storedVersion int64
	var jsonIDs string
	requireNoError(t, db.QueryRow(`SELECT version,COALESCE(
 (SELECT json_agg(user_id::text ORDER BY user_id) FROM public.content_item_assignments WHERE item_id=i.id),'[]'::json
 )::text FROM public.content_items i WHERE id=$1`, itemID).Scan(&storedVersion, &jsonIDs))
	var actual []string
	requireNoError(t, json.Unmarshal([]byte(jsonIDs), &actual))
	expected := slices.Clone(ids)
	slices.Sort(expected)
	if storedVersion != version || !slices.Equal(actual, expected) {
		t.Fatalf("item %s has version %d and assignees %v, expected version %d and assignees %v", itemID, storedVersion, actual, version, expected)
	}
}

func TestAuthContentAssigneesEnvelopeIntegration(t *testing.T) {
	x := newAssigneeFixture(t)
	f := x.http.state
	ctx := context.Background()
	recipient := x.recipient.user.ID

	_, err := x.replace(ctx, 1, recipient, strings.ToUpper(recipient))
	onboardingError(t, err, identity.ErrValidation)
	_, err = x.replace(ctx, 1, assigneeMissingID)
	onboardingError(t, err, identity.ErrNotFound)
	_, err = x.replace(ctx, 1, f.user)
	onboardingError(t, err, identity.ErrNotFound)
	assertItemAssignees(t, f.db, assigneeItem, 1)

	// An assign grant and a read envelope cannot activate latent update access.
	latent := createAssignedTestRole(t, f, "Disabled assignment updater", lifecycleHTTPDisabledState, recipient)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES(gen_random_uuid(),$1,'content.items.update','content',$2,'assigned')`, latent, assigneeTypeID)
	requireNoError(t, err)
	_, err = x.replace(ctx, 1, recipient)
	onboardingError(t, err, identity.ErrNotFound)
	assertItemAssignees(t, f.db, assigneeItem, 1)
	_, err = f.db.Exec(`DELETE FROM public.role_grants WHERE role_id=$1`, latent)
	requireNoError(t, err)

	// Replacing a set may remove an existing assignment held by the actor.
	_, err = f.db.Exec(`INSERT INTO public.content_item_assignments(item_id,user_id) VALUES($1,$2)`, assigneeItem, x.manager.user.ID)
	requireNoError(t, err)
	_, err = x.replace(ctx, 1, recipient)
	requireNoError(t, err)
	assertItemAssignees(t, f.db, assigneeItem, 2, recipient)
	assertItemAssignees(t, f.db, assigneeOtherItem, 1)
	assertMembershipState(t, f, x.manager.user, x.manager.user.Version, []string{x.managerRole})
	assertMembershipState(t, f, x.recipient.user, x.recipient.user.Version, []string{x.reader, latent})

	// The old assignee is protected by the envelope even when a stale version is supplied.
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES(gen_random_uuid(),$1,'content.items.update','content',$2,'assigned')`, latent, assigneeTypeID)
	requireNoError(t, err)
	_, err = x.replace(ctx, 1)
	onboardingError(t, err, identity.ErrNotFound)
	assertItemAssignees(t, f.db, assigneeItem, 2, recipient)
}

func TestAuthContentAssigneesRevocationOrderIntegration(t *testing.T) {
	for _, assignmentFirst := range []bool{false, true} {
		name := "revocation first"
		if assignmentFirst {
			name = "assignment first"
		}
		t.Run(name, func(t *testing.T) {
			x := newAssigneeFixture(t)
			f := x.http.state
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			revoker := newRestrictedAuthService(t, f)
			lock := holdInstallationWriteMutex(t, f.db)
			assign := func() error {
				_, err := x.replace(ctx, 1, x.recipient.user.ID)
				return err
			}
			revoke := func() error {
				_, err := revoker.ReplaceUserRoles(ctx, authReq(x.admin), x.manager.user.ID, x.manager.user.Version, []string{})
				return err
			}
			first, second := revoke, assign
			if assignmentFirst {
				first, second = assign, revoke
			}
			a, b := make(chan error, 1), make(chan error, 1)
			go func() { a <- first() }()
			waitForInstallationWriteWaiters(t, f.db, 1)
			go func() { b <- second() }()
			waitForInstallationWriteWaiters(t, f.db, 2)
			requireNoError(t, lock.Commit())
			requireNoError(t, <-a)
			if assignmentFirst {
				requireNoError(t, <-b)
				assertItemAssignees(t, f.db, assigneeItem, 2, x.recipient.user.ID)
			} else {
				onboardingError(t, <-b, identity.ErrPermissionDenied)
				assertItemAssignees(t, f.db, assigneeItem, 1)
			}
			assertMembershipState(t, f, x.manager.user, x.manager.user.Version+1, nil)
		})
	}
}

func TestAuthContentAssigneesAuditRollbackIntegration(t *testing.T) {
	x := newAssigneeFixture(t)
	f := x.http.state
	ctx := context.Background()
	_, err := x.replace(ctx, 1, x.recipient.user.ID)
	requireNoError(t, err)
	_, err = f.db.Exec(`CREATE FUNCTION public.fail_assignment_audit() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.operation='content.assignees_replaced' AND NEW.result='success' THEN
   RAISE EXCEPTION 'unavailable assignment audit';
  END IF;
  RETURN NEW;
 END $$;
 CREATE TRIGGER fail_assignment_audit BEFORE INSERT ON public.security_audit
 FOR EACH ROW EXECUTE FUNCTION public.fail_assignment_audit()`)
	requireNoError(t, err)
	_, err = x.replace(ctx, 2)
	onboardingError(t, err, identity.ErrSecurityUnavailable)
	assertItemAssignees(t, f.db, assigneeItem, 2, x.recipient.user.ID)
	var count int
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.security_audit
 WHERE operation='content.assignees_replaced' AND result='success' AND target_id=$1`, assigneeItem).Scan(&count))
	if count != 1 {
		t.Fatalf("stored %d successful assignment events, expected one", count)
	}
}

func TestAuthContentAssigneesRestrictedPrivilegesIntegration(t *testing.T) {
	x := newAssigneeFixture(t)
	ctx := context.Background()
	cfg := testpostgres.AdminConfig(t)
	requireNoError(t, x.http.state.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_auth"
	pool := database.Open(cfg)
	defer func() { _ = pool.Close() }()
	for _, statement := range []string{
		`UPDATE public.content_items SET published_id=NULL WHERE id=$1`,
		`UPDATE public.content_item_assignments SET user_id=user_id WHERE item_id=$1`,
		`UPDATE public.content_drafts SET shared_values='{}' WHERE item_id=$1`,
		`SELECT shared_values FROM public.content_publications WHERE item_id=$1`,
	} {
		err := identity.WithWriteMutex(ctx, pool, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement, assigneeItem)
			return err
		})
		assertSQLState(t, err, "42501")
	}
}

func TestAuthContentAssigneesRequiresAnyScopeIntegration(t *testing.T) {
	x := newAssigneeFixture(t)
	f := x.http.state
	ctx := context.Background()
	_, err := f.db.Exec(`INSERT INTO public.content_item_assignments(item_id,user_id) VALUES($1,$2)`,
		assigneeItem, x.recipient.user.ID)
	requireNoError(t, err)
	_, err = f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES
 (gen_random_uuid(),$1,'content.items.assign','content',$2,'assigned')`, x.reader, assigneeTypeID)
	requireNoError(t, err)
	actor := lifecycleLoginFull(t, f, x.recipient)
	_, err = f.service.ReplaceContentAssignees(ctx, authReq(actor), assigneeTypeKey, assigneeItem, 1, []string{})
	onboardingError(t, err, identity.ErrPermissionDenied)
	assertItemAssignees(t, f.db, assigneeItem, 1, x.recipient.user.ID)
}
