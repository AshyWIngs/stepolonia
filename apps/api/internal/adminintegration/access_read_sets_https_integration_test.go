package adminintegration

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"testing"
)

func TestAdminRoleMembershipSetHTTPSIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	state := f.state
	ctx := context.Background()
	admin := authFull(t, state)
	manager := lifecycleEnrolled(t, state, admin, "membership-reader@example.test")
	var err error
	manager.user, err = state.service.ActivateUser(ctx, authReq(admin), manager.user.ID, manager.user.Version)
	requireNoError(t, err)
	actor := lifecycleLoginFull(t, state, manager)
	target := lifecycleEnrolled(t, state, admin, "membership-target@example.test")
	managerRole := createAssignedTestRole(t, state, "Membership manager", lifecycleHTTPActiveState, manager.user.ID)
	targetRole := createAssignedTestRole(t, state, "Retained readership", lifecycleHTTPDisabledState, target.user.ID)
	hiddenRole := createMembershipTestRole(t, state, "Unassignable role", lifecycleHTTPActiveState)
	emptyRole := createMembershipTestRole(t, state, "Unconfigured role", lifecycleHTTPDisabledState)
	_, err = state.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,can_delegate) VALUES
 (gen_random_uuid(),$1,'roles.assign',false), (gen_random_uuid(),$1,'users.read',true),
 (gen_random_uuid(),$2,'users.read',false), (gen_random_uuid(),$3,'roles.edit',false)`, managerRole, targetRole, hiddenRole)
	requireNoError(t, err)

	path := onboardingUsersPath + "/" + target.user.ID + "/roles"
	expireSessionStepUp(t, state.db, actor.Token)
	out := f.lifecycleRead(t, path, actor, http.StatusOK)
	if out["user_id"] != target.user.ID || out["version"] != float64(target.user.Version) ||
		out["max_roles"] != float64(16) || !reflect.DeepEqual(out["role_ids"], []any{targetRole}) {
		t.Fatalf("membership set does not preserve the existing account revision: %v", out)
	}
	options := readSetOptions(t, out, 4)
	if row := options[targetRole]; row["name"] != "Retained readership" || row["state"] != lifecycleHTTPDisabledState || row["domain"] != "access" {
		t.Fatalf("current role was not named and grouped: %v", row)
	}
	if row := options[emptyRole]; row["domain"] != "other" {
		t.Fatalf("role without grants lacked a separate category: %v", row)
	}
	if options[hiddenRole] != nil || options[managerRole] != nil {
		t.Fatal("role outside the delegation envelope was exposed")
	}

	missing := f.lifecycleRead(t, onboardingUsersPath+"/"+assigneeMissingID+"/roles", actor, http.StatusNotFound)
	superior := f.lifecycleRead(t, onboardingUsersPath+"/"+state.user+"/roles", actor, http.StatusNotFound)
	if !reflect.DeepEqual(missing, superior) {
		t.Fatal("missing and superior accounts have distinguishable responses")
	}
	requireNoError(t, state.service.Logout(ctx, authReq(actor), actor.Kind))
	f.lifecycleRead(t, path, actor, http.StatusUnauthorized)
}

func TestAdminContentAssignmentSetHTTPSIntegration(t *testing.T) {
	x := newAssigneeFixture(t)
	f := x.http
	state := f.state
	ctx := context.Background()
	path := draftHTTPPrefix + assigneeTypeKey + "/" + assigneeItem + "/assignees"
	recipientID := x.recipient.user.ID

	_, err := x.replace(ctx, 1, recipientID)
	requireNoError(t, err)
	_, err = state.service.DisableUser(ctx, authReq(x.admin), recipientID, x.recipient.user.Version)
	requireNoError(t, err)
	expireSessionStepUp(t, state.db, x.actor.Token)

	out := f.lifecycleRead(t, path, x.actor, http.StatusOK)
	if out["item_id"] != assigneeItem || out["version"] != "2" || out["max_assignees"] != float64(100) ||
		!reflect.DeepEqual(out["assignee_ids"], []any{recipientID}) {
		t.Fatalf("assignment set lost the complete current revision: %v", out)
	}
	options := readSetOptions(t, out, 3)
	if row := options[recipientID]; row["login"] != x.recipient.user.Login || row["state"] != lifecycleHTTPDisabledState {
		t.Fatal("inactive assignee was omitted or cannot be identified for removal")
	}
	if options[x.manager.user.ID] != nil || options[state.user] != nil {
		t.Fatal("unassigned actor or superior account was offered as an assignee")
	}
	otherPath := draftHTTPPrefix + assigneeOtherKey + "/" + assigneeOtherItem + "/assignees"
	f.lifecycleRead(t, otherPath, x.actor, http.StatusNotFound)
	f.lifecycleRead(t, path+"?limit=1", x.actor, http.StatusBadRequest)

	// The existing assignment is now outside the envelope. The read must hide
	// the whole set rather than present a misleading partial replacement.
	_, err = state.db.Exec(`UPDATE public.role_grants SET operation='content.items.update' WHERE role_id=$1 AND type_id=$2`, x.reader, assigneeTypeID)
	requireNoError(t, err)
	missing := f.lifecycleRead(t, draftHTTPPrefix+assigneeTypeKey+"/"+assigneeMissingID+"/assignees", x.actor, http.StatusNotFound)
	hidden := f.lifecycleRead(t, path, x.actor, http.StatusNotFound)
	if !reflect.DeepEqual(missing, hidden) {
		t.Fatal("the existence of excluded recipient assignments can be inferred")
	}
	assertItemAssignees(t, state.db, assigneeItem, 2, recipientID)
}

func readSetOptions(t *testing.T, out map[string]any, fields int) map[string]map[string]any {
	t.Helper()
	options, ok := out["options"].([]any)
	if !ok {
		t.Fatalf("missing named options: %v", out)
	}
	result := make(map[string]map[string]any, len(options))
	for _, option := range options {
		row, ok := option.(map[string]any)
		if !ok || len(row) != fields {
			t.Fatalf("unexpected option shape: %v", option)
		}
		id, ok := row["id"].(string)
		if !ok || id == "" || result[id] != nil || !slices.Contains([]any{lifecycleHTTPActiveState, lifecycleHTTPDisabledState, "archived"}, row["state"]) {
			t.Fatalf("unexpected option: %v", option)
		}
		result[id] = row
	}
	return result
}
