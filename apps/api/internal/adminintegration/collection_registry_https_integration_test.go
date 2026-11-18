package adminintegration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/platform"
)

const (
	managedCollectionsPath = "/api/v1/admin/content-types"
	eventCollectionPath    = managedCollectionsPath + "/event"
	eventBaseFields        = `{"key":"title","kind":"string","scope":"localized","visibility":"public","required":true,"config":{"min_length":1}},
 {"key":"slug","kind":"string","scope":"localized","visibility":"public","required":true,"config":{"slug":true,"max_length":200}}`
	eventExtraField = `{"key":"subtitle","kind":"string","scope":"localized","visibility":"private","required":false}`
	eventCreateBody = `{"route_namespace":"events","initial_access":true,"fields":[` + eventBaseFields + `]}`
)

func TestAdminCollectionInitialAccessHTTPSIntegration(t *testing.T) {
	f := newAuthHTTPFixtureWithEditors(t, func(_ authFixtureState, auth http.Handler) http.Handler {
		return platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	}, true)
	actor := authReq(authFull(t, f.state))
	call := func(method, path, payload string, status int) string {
		t.Helper()
		return sendDraftRequest(t, f.server, method, actor.Token, draftHTTPRequest{path: path, body: payload, csrf: actor.CSRF, status: status})
	}

	renderers := call(http.MethodGet, managedCollectionsPath+"/renderers", "", http.StatusOK)
	if !strings.Contains(renderers, `"type_key":"event","route_namespace":"events","available":true`) {
		t.Fatalf("expected Event renderer to be available: %s", renderers)
	}
	expireSessionStepUp(t, f.state.db, actor.Token)
	call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusForbidden)
	authRefreshStep(t, f.state.db, actor.Token)
	call(http.MethodPost, managedCollectionsPath, strings.Replace(eventCreateBody, `"route_namespace"`, `"type_id":"injected","route_namespace"`, 1), http.StatusBadRequest)
	verifyRegistryAuditRollback(t, f.state, func() {
		call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusServiceUnavailable)
	})

	created := call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusCreated)
	if !strings.Contains(created, `"type_key":"event"`) || !strings.Contains(created, `"schema_no":"1"`) {
		t.Fatalf("wrong collection receipt: %s", created)
	}
	typeID, roleID := assertInitialEventGrants(t, f.state)
	call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusConflict)
	call(http.MethodPost, draftHTTPPrefix+"event", createDraftHTTPSBody, http.StatusCreated)
	const revision = `{"expected_version":"1","initialize_new_field_access":true,"fields":[` + eventBaseFields + `,` + eventExtraField + `]}`
	call(http.MethodPatch, eventCollectionPath, revision, http.StatusOK)
	assertAddedFieldGrants(t, f.state, typeID, roleID)
	call(http.MethodPatch, eventCollectionPath, revision, http.StatusConflict)
	call(http.MethodPatch, eventCollectionPath, strings.Replace(revision, `"expected_version":"1"`, `"expected_version":"2"`, 1), http.StatusForbidden)
	assertUninitializedFieldAvailableToManagers(t, f.state, typeID, call)
	assertBoundRoleGuard(t, f.state, typeID, roleID)
}

func assertUninitializedFieldAvailableToManagers(t *testing.T, state authFixtureState, typeID string, call func(string, string, string, int) string) {
	t.Helper()
	const extra = `{"key":"internal_note","kind":"text","scope":"localized","visibility":"private","required":false}`
	const revise = `{"expected_version":"2","fields":[` + eventBaseFields + `,` + eventExtraField + `,` + extra + `]}`
	revised := call(http.MethodPatch, eventCollectionPath, revise, http.StatusOK)
	var fieldID string
	var grants int
	requireNoError(t, state.db.QueryRow(`SELECT f.field_id::text,
	 (SELECT count(*) FROM public.grant_fields g WHERE g.field_id=f.field_id AND g.type_id=f.type_id)
	 FROM public.content_fields f WHERE f.type_id=$1 AND f.key='internal_note'`, typeID).Scan(&fieldID, &grants))
	read := call(http.MethodGet, eventCollectionPath, "", http.StatusOK)
	idJSON := fmt.Sprintf(`"field_id":%q`, fieldID)
	if grants != 0 || !strings.Contains(revised, idJSON) || !strings.Contains(read, idJSON) {
		t.Fatalf("uninitialized field: grants=%d, revised=%s, read=%s", grants, revised, read)
	}
}

func verifyRegistryAuditRollback(t *testing.T, state authFixtureState, create func()) {
	t.Helper()
	_, err := state.db.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_initial_access_audit CHECK (operation<>'content.initial_access_granted')`)
	requireNoError(t, err)
	create()
	_, err = state.db.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_initial_access_audit`)
	requireNoError(t, err)
	var count int
	requireNoError(t, state.db.QueryRow(`SELECT count(*) FROM public.content_types WHERE key='event'`).Scan(&count))
	if count != 0 {
		t.Fatal("collection creation survived failed Q25 audit")
	}
}

func assertInitialEventGrants(t *testing.T, state authFixtureState) (string, string) {
	t.Helper()
	var typeID, roleID string
	var grants, fieldGrants, memberships, events int
	requireNoError(t, state.db.QueryRow(`SELECT t.id::text,r.id::text,
 (SELECT count(*) FROM public.role_grants g WHERE g.role_id=r.id AND NOT g.can_delegate AND g.type_id=t.id),
 (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON g.id=f.grant_id WHERE g.role_id=r.id),
 (SELECT count(*) FROM public.user_roles m WHERE m.role_id=r.id AND m.user_id=$1),
 (SELECT count(*) FROM public.security_audit a WHERE a.actor_id=$1 AND a.target_id=t.id
  AND a.operation='content.initial_access_granted' AND a.details->>'reason'='explicit_initial_access' AND a.details->>'role_id'=r.id::text)
 FROM public.content_types t JOIN public.roles r ON r.self_access_type_id=t.id AND r.self_access_user_id=$1
 WHERE t.key='event' AND r.state='active'`, state.user).Scan(&typeID, &roleID, &grants, &fieldGrants, &memberships, &events))
	if grants != 6 || fieldGrants != 10 || memberships != 1 || events != 1 {
		t.Fatalf("invalid initial access: grants=%d fields=%d memberships=%d audit=%d", grants, fieldGrants, memberships, events)
	}
	return typeID, roleID
}

func assertAddedFieldGrants(t *testing.T, state authFixtureState, typeID, roleID string) {
	t.Helper()
	var exact, all, events int
	requireNoError(t, state.db.QueryRow(`SELECT
 (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON f.grant_id=g.id
  JOIN public.content_fields d ON d.type_id=f.type_id AND d.field_id=f.field_id
  WHERE g.role_id=$1 AND d.type_id=$2 AND d.key='subtitle' AND g.object_scope='any' AND NOT g.can_delegate
  AND g.operation IN ('content.items.read','content.items.update','content.items.ready','content.items.publish')),
 (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON f.grant_id=g.id WHERE g.role_id=$1),
 (SELECT count(*) FROM public.security_audit WHERE target_id=$2 AND operation='content.new_field_access_granted'
  AND details->>'role_id'=$1::text)`, roleID, typeID).Scan(&exact, &all, &events))
	if exact != 4 || all != 14 || events != 1 {
		t.Fatalf("invalid new field access: exact=%d all=%d audit=%d", exact, all, events)
	}
}

func assertBoundRoleGuard(t *testing.T, state authFixtureState, typeID, roleID string) {
	t.Helper()
	_, err := state.db.ExecContext(context.Background(), `INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES(gen_random_uuid(),$1,'content.items.assign','content',$2,'any')`, roleID, typeID)
	if err == nil {
		t.Fatal("bound role allowed the assignment operation")
	}
	_, err = state.db.Exec(`UPDATE public.roles SET self_access_type_id=NULL,self_access_user_id=NULL WHERE id=$1`, roleID)
	if err == nil {
		t.Fatal("bound role identity was mutable")
	}
}
