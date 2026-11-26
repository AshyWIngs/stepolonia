package adminintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

const rolesHTTPPath = "/api/v1/admin/roles"
const stepUpRequiredProblem = "step_up_required"

func (f authHTTPFixture) roleHTTP(t *testing.T, method, path, body string, session identity.SessionTokens, status int) (map[string]any, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, f.server.URL, session.CSRF)
	req.Header.Set(authRequestIDHeader, "forged-client-id")
	req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: session.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	client := f.server.Client()
	client.Jar = nil
	res, err := client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != status {
		t.Fatalf("private role %s %s status=%d expected=%d", method, path, res.StatusCode, status)
	}
	assertAuthNoStore(t, res)
	if len(res.Cookies()) != 0 {
		t.Fatal("role endpoint changed session cookies")
	}
	out := map[string]any{}
	requireNoError(t, json.NewDecoder(res.Body).Decode(&out))
	id := res.Header.Get("X-Request-ID")
	if id == "" || id == "forged-client-id" {
		t.Fatal("role request ID trusts client input")
	}
	return out, id
}

func TestAuthRolesHTTPAtomicEditorIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	created, requestID := f.roleHTTP(t, http.MethodPost, rolesHTTPPath, `{"name":"http-policy","grants":[{"operation":"security_policy.manage","scope":"installation","can_delegate":false}]}`, full, 200)
	if len(created) != 5 || created["state"] != "disabled" || created["version"] != float64(1) {
		t.Fatal("role creation projection/state/version drifted")
	}
	id := created["id"].(string)
	assertRoleCreationHTTP(t, f, id, requestID)
	path := rolesHTTPPath + "/" + id
	f.roleHTTP(t, http.MethodGet, path, "", full, 200)
	list, _ := f.roleHTTP(t, http.MethodGet, rolesHTTPPath+"?limit=1&offset=0", "", full, 200)
	if len(list) != 2 || len(list["items"].([]any)) != 1 || list["next_offset"] != float64(1) {
		t.Fatal("roles bounded pagination drifted")
	}
	edited, _ := f.roleHTTP(t, http.MethodPatch, path, `{"name":"http-policy-edited","grants":[],"expected_version":1}`, full, 200)
	if edited["version"] != float64(2) || edited["state"] != "disabled" || len(edited["grants"].([]any)) != 0 {
		t.Fatal("PATCH did not replace grants atomically")
	}
	stale, _ := f.roleHTTP(t, http.MethodPatch, path, `{"name":"stale-write","grants":[],"expected_version":1}`, full, 409)
	if stale["code"] != "version_conflict" {
		t.Fatal("stale role version lost machine code")
	}
	assertMalformedRolesHTTP(t, f, full)
	got, _ := f.roleHTTP(t, http.MethodGet, path, "", full, 200)
	if got["name"] != "http-policy-edited" || got["version"] != float64(2) || len(got["grants"].([]any)) != 0 {
		t.Fatal("failed HTTP operation changed role policy")
	}
}

func TestAuthRolesHTTPScopedChangesIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	created, _ := f.roleHTTP(t, http.MethodPost, rolesHTTPPath,
		`{"name":"scoped-reviewer","grants":[{"operation":"users.read","scope":"installation","can_delegate":false},{"operation":"audit.read","scope":"installation","can_delegate":false}]}`,
		full, http.StatusOK)
	id := created["id"].(string)
	path := rolesHTTPPath + "/" + id + "/changes"
	untouched := roleAccountReadGrantID(t, f, id)

	delta := `{"expected_version":1,"upsert":[{"operation":"security_policy.manage","scope":"installation","can_delegate":false}],"remove":[{"operation":"audit.read"}]}`
	noCSRF := full
	noCSRF.CSRF = ""
	f.roleHTTP(t, http.MethodPost, path, delta, noCSRF, http.StatusForbidden)
	scoped, requestID := f.roleHTTP(t, http.MethodPost, path, delta, full, http.StatusOK)
	if scoped["name"] != created["name"] || scoped["version"] != float64(2) || len(scoped["grants"].([]any)) != 2 {
		t.Fatal("grant delta did not preserve the role name and untouched grant")
	}
	if roleAccountReadGrantID(t, f, id) != untouched {
		t.Fatal("grant delta replaced an untouched row")
	}
	var audited bool
	requireNoError(t, f.state.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.security_audit
WHERE target_id=$1 AND operation='roles.edited' AND request_id=$2)`, id, requestID).Scan(&audited))
	if !audited {
		t.Fatal("role changes lost audit correlation")
	}
	f.roleHTTP(t, http.MethodPost, path, delta, full, http.StatusConflict)
	assertMalformedRoleChanges(t, f, path, full)
	renamed, _ := f.roleHTTP(t, http.MethodPost, path, `{"expected_version":2,"name":"policy-reviewer"}`, full, http.StatusOK)
	if renamed["name"] != "policy-reviewer" || !reflect.DeepEqual(renamed["grants"], scoped["grants"]) ||
		roleAccountReadGrantID(t, f, id) != untouched {
		t.Fatal("renaming replaced the grant policy")
	}
	expireSessionStepUp(t, f.state.db, full.Token)
	body, _ := f.roleHTTP(t, http.MethodPost, path, `{"expected_version":3,"name":"expired-session"}`, full, http.StatusForbidden)
	if body["code"] != stepUpRequiredProblem {
		t.Fatal("sparse role edit did not require fresh MFA")
	}
}

func TestAuthRoleDisplayNameHTTPSIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	_, err := f.state.db.Exec(`UPDATE public.languages SET is_enabled=true WHERE code=$1`, "pl")
	requireNoError(t, err)
	admin := authFull(t, f.state)
	created, _ := f.roleHTTP(t, http.MethodPost, rolesHTTPPath, `{"name":"Reviewers","grants":[]}`, admin, http.StatusOK)
	id := created["id"].(string)
	path := rolesHTTPPath + "/" + id
	labelling := path + "/display-name"
	const body = `{"expected_version":1,"language_code":"pl","display_name":"Recenzenci"}`
	noCSRF := admin
	noCSRF.CSRF = ""
	f.roleHTTP(t, http.MethodPost, labelling, body, noCSRF, http.StatusForbidden)
	for _, input := range []string{
		`{"expected_version":1,"language_code":"pl"}`,
		`{"expected_version":1,"language_code":"pl","display_name":null,"display_name":"Duplicated"}`,
		`{"expected_version":1,"language_code":"unknown","display_name":"Recenzenci"}`,
		`{"expected_version":1,"language_code":"pl","display_name":" Recenzenci "}`,
	} {
		f.roleHTTP(t, http.MethodPost, labelling, input, admin, http.StatusBadRequest)
	}
	saved, requestID := f.roleHTTP(t, http.MethodPost, labelling, body, admin, http.StatusOK)
	if saved["version"] != float64(2) || saved["name"] != created["name"] || !reflect.DeepEqual(saved["grants"], created["grants"]) {
		t.Fatal("setting a role display name changed stored policy")
	}
	if !reflect.DeepEqual(saved["display_names"], map[string]any{"pl": "Recenzenci"}) {
		t.Fatal("new role label was not returned")
	}
	var audited bool
	requireNoError(t, f.state.db.QueryRow(`SELECT EXISTS(
 SELECT 1 FROM public.security_audit WHERE target_id=$1 AND operation='roles.edited' AND request_id=$2
 )`, id, requestID).Scan(&audited))
	if !audited {
		t.Fatal("setting a role display name lacked atomic audit correlation")
	}
	f.roleHTTP(t, http.MethodPost, labelling, body, admin, http.StatusConflict)
	details, _ := f.roleHTTP(t, http.MethodGet, path, "", admin, http.StatusOK)
	if !reflect.DeepEqual(details["display_names"], saved["display_names"]) {
		t.Fatal("role read omitted the saved localized label")
	}
	membership := f.lifecycleRead(t, onboardingUsersPath+"/"+f.state.user+"/roles", admin, http.StatusOK)
	found := false
	for _, option := range membership["options"].([]any) {
		row := option.(map[string]any)
		if row["id"] == id && reflect.DeepEqual(row["display_names"], saved["display_names"]) {
			found = true
		}
	}
	if !found {
		t.Fatal("membership options omitted the saved localized label")
	}
	removed, _ := f.roleHTTP(t, http.MethodPost, labelling, `{"expected_version":2,"language_code":"pl","display_name":null}`, admin, http.StatusOK)
	if _, exists := removed["display_names"]; exists || removed["version"] != float64(3) {
		t.Fatal("deleting a localized role label did not restore base-name fallback")
	}
	expireSessionStepUp(t, f.state.db, admin.Token)
	stale, _ := f.roleHTTP(t, http.MethodPost, labelling, `{"expected_version":3,"language_code":"pl","display_name":"Recenzenci"}`, admin, http.StatusForbidden)
	if stale["code"] != stepUpRequiredProblem {
		t.Fatal("role display name command did not require fresh MFA")
	}
}

func assertMalformedRoleChanges(t *testing.T, f authHTTPFixture, path string, full identity.SessionTokens) {
	t.Helper()
	for _, body := range []string{
		`{"expected_version":137}`,
		`{"expected_version":2,"name":null}`,
		`{"expected_version":2,"name":"reviewer","Name":"other"}`,
		`{"expected_version":2,"upsert":[{"operation":"users.read","Operation":"audit.read","scope":"installation","can_delegate":false}]}`,
		`{"expected_version":2,"remove":[{"operation":"users.read","Operation":"audit.read"}]}`,
		`{"expected_version":2,"remove":[{"operation":"users.read"}],"upsert":[{"operation":"users.read","scope":"installation","can_delegate":false}]}`,
	} {
		f.roleHTTP(t, http.MethodPost, path, body, full, http.StatusBadRequest)
	}
}

func roleAccountReadGrantID(t *testing.T, f authHTTPFixture, roleID string) string {
	t.Helper()
	var id string
	requireNoError(t, f.state.db.QueryRow(`SELECT id::text FROM public.role_grants WHERE role_id=$1 AND operation='users.read'`, roleID).Scan(&id))
	return id
}

func assertRoleCreationHTTP(t *testing.T, f authHTTPFixture, id, requestID string) {
	t.Helper()
	var isolated, audited bool
	requireNoError(t, f.state.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.user_roles WHERE role_id=$1),EXISTS(SELECT 1 FROM public.security_audit WHERE target_id=$1 AND operation='roles.created' AND request_id=$2)`, id, requestID).Scan(&isolated, &audited))
	if !isolated || !audited {
		t.Fatal("HTTP create assigned recipients or lost audit correlation")
	}
}

func assertMalformedRolesHTTP(t *testing.T, f authHTTPFixture, full identity.SessionTokens) {
	t.Helper()
	for _, body := range []string{
		`{"name":"bad-policy","grants":[{"operation":"unknown.operation","scope":"installation","can_delegate":false}]}`,
		`{"name":"bad-policy","grants":[{"operation":"users.read","scope":"installation","can_delegate":false,"can_delegate":true}]}`,
		`{"name":"bad-policy","grants":[{"operation":"content.items.read","scope":"content","can_delegate":false,"type_id":"00000000-0000-4000-8000-000000000099","object_scope":"any"}]}`,
		`{"name":"bad-policy","grants":[{"operation":"users.read","scope":"installation","can_delegate":false,"field_ids":[]}]}`,
		`{"name":"bad-policy","grants":[],"collection_keys":{"00000000-0000-4000-8000-000000000099":"article"}}`,
	} {
		f.roleHTTP(t, http.MethodPost, rolesHTTPPath, body, full, 400)
	}
}

func TestAuthRolesHTTPReadOnlySessionIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	account := lifecycleEnrolled(t, f.state, full, "role-reader@example.test")
	var err error
	account.user, err = f.state.service.ActivateUser(context.Background(), authReq(full), account.user.ID, account.user.Version)
	requireNoError(t, err)
	roleID := createAssignedTestRole(t, f.state, "Policy reader", lifecycleHTTPActiveState, account.user.ID)
	_, err = f.state.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation) VALUES(gen_random_uuid(),$1,'roles.read')`, roleID)
	requireNoError(t, err)
	reader := lifecycleLoginFull(t, f.state, account)
	_, err = f.state.db.Exec(`UPDATE public.sessions SET step_up_at=NULL WHERE token_digest IN ($1,$2)`, authDigest(reader.Token), authDigest(full.Token))
	requireNoError(t, err)
	var superior string
	requireNoError(t, f.state.db.QueryRow(`SELECT id::text FROM public.roles WHERE name='Site Admin'`).Scan(&superior))
	assertRoleCollectionKeysHTTP(t, f, reader, superior)
	f.roleHTTP(t, http.MethodGet, rolesHTTPPath, "", full, 200)
	stale, _ := f.roleHTTP(t, http.MethodPost, rolesHTTPPath, `{"name":"stale-admin","grants":[]}`, full, 403)
	if stale["code"] != stepUpRequiredProblem {
		t.Fatal("stale administrator bypassed role freshness")
	}
	problem, _ := f.roleHTTP(t, http.MethodPost, rolesHTTPPath, `{"name":"forbidden","grants":[]}`, reader, 403)
	if problem["code"] != "permission_denied" {
		t.Fatal("reader acquired create permission")
	}
	authRefreshStep(t, f.state.db, reader.Token)
	problem, _ = f.roleHTTP(t, http.MethodPatch, rolesHTTPPath+"/"+superior, `{"name":"forbidden","grants":[],"expected_version":1}`, reader, 403)
	if problem["code"] != "permission_denied" {
		t.Fatal("reader acquired edit permission")
	}
}

func assertRoleCollectionKeysHTTP(t *testing.T, f authHTTPFixture, reader identity.SessionTokens, roleID string) {
	t.Helper()
	_, err := f.state.db.Exec(`WITH new_types AS (
 INSERT INTO public.content_types(id,key,route_namespace)
 VALUES ($1,'editorial_article','editorial-article'),($2,'other_collection','other-collection') RETURNING id
) INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM new_types`, lifecycleType, lifecycleOtherType)
	requireNoError(t, err)
	_, err = f.state.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES(gen_random_uuid(),$1,'content.items.read','content',$2,'assigned'),
 (gen_random_uuid(),$1,'content.items.update','content',$2,'assigned')`, roleID, lifecycleType)
	requireNoError(t, err)
	wantKeys := map[string]any{lifecycleType: "editorial_article"}
	out, _ := f.roleHTTP(t, http.MethodGet, rolesHTTPPath+"/"+roleID, "", reader, 200)
	if len(out) != 6 || len(out["grants"].([]any)) < 2 || !reflect.DeepEqual(out["collection_keys"], wantKeys) {
		t.Fatalf("role projection has unexpected collection keys: %v", out["collection_keys"])
	}
	page, _ := f.roleHTTP(t, http.MethodGet, rolesHTTPPath, "", reader, 200)
	var listed any
	for _, item := range page["items"].([]any) {
		entry := item.(map[string]any)
		if entry["id"] == roleID {
			listed = entry["collection_keys"]
		}
	}
	if !reflect.DeepEqual(listed, wantKeys) {
		t.Fatalf("listed role has unexpected collection keys: %v", listed)
	}
}
