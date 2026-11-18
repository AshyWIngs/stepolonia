package cmshttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/cms"
)

type fakeRoles struct {
	fakeOnboarding
	page      cms.RolePage
	policy    cms.RoleInput
	operation string
}

func (s *fakeRoles) ListRoles(_ context.Context, req cms.AuthRequest, page cms.RolePage) (cms.RoleList, error) {
	s.calls++
	s.req, s.page, s.operation = req, page, listRolesOperation
	return cms.RoleList{Items: []cms.Role{{ID: syntheticUserID, Name: "safe-policy", State: lifecycleFixtureState, Version: 3, Grants: []cms.RoleGrant{{Operation: "users.read", Scope: roleInstallationScope}}}}}, s.commandErr
}

func (s *fakeRoles) GetRole(_ context.Context, req cms.AuthRequest, id string) (cms.Role, error) {
	s.calls++
	s.req, s.userID, s.operation = req, id, getRoleOperation
	return cms.Role{ID: id, Name: "safe-policy", State: lifecycleFixtureState, Version: 3, Grants: []cms.RoleGrant{}}, s.commandErr
}

func (s *fakeRoles) CreateRole(_ context.Context, req cms.AuthRequest, input cms.RoleInput) (cms.Role, error) {
	s.calls++
	s.req, s.policy, s.operation = req, input, createRoleOperation
	return cms.Role{ID: syntheticUserID, Name: input.Name, State: lifecycleFixtureState, Version: 1, Grants: input.Grants}, s.commandErr
}

func (s *fakeRoles) EditRole(_ context.Context, req cms.AuthRequest, id string, version int64, input cms.RoleInput) (cms.Role, error) {
	s.calls++
	s.req, s.userID, s.version, s.policy, s.operation = req, id, version, input, editRoleOperation
	return cms.Role{ID: id, Name: input.Name, State: lifecycleFixtureState, Version: version + 1, Grants: input.Grants}, s.commandErr
}

func roleRequest(t *testing.T, server *httptest.Server, method, path, body, kind string) *http.Request {
	t.Helper()
	req := lifecycleGET(t, server, path, kind)
	req.Method = method
	if method != http.MethodGet {
		req.Body = io.NopCloser(strings.NewReader(body))
		req.Header.Set(originHeader, server.URL)
		req.Header.Set(contentTypeHeader, jsonMediaType)
		req.Header.Set(testCSRFHeader, testToken)
	}
	return req
}

func TestAuthRolesDispatchAndSafeProjection(t *testing.T) {
	service := &fakeRoles{}
	server := onboardingTLSServer(t, service)
	for _, tc := range []struct{ method, path, body, operation string }{
		{http.MethodGet, rolesPath + "?limit=2&offset=1", "", listRolesOperation},
		{http.MethodGet, rolesPath + "/" + syntheticUserID, "", getRoleOperation},
		{http.MethodPost, rolesPath, `{"name":"new-role","grants":[{"operation":"users.read","scope":"installation","can_delegate":false}]}`, createRoleOperation},
		{http.MethodPatch, rolesPath + "/" + syntheticUserID, `{"name":"edited-role","grants":[],"expected_version":3}`, editRoleOperation},
	} {
		req := roleRequest(t, server, tc.method, tc.path, tc.body, fullKind)
		req.Header.Set("X-Request-ID", "forged-request")
		out := lifecycleResponse(t, server, req, 200)
		if service.req.RequestID == "" || service.req.RequestID == "forged-request" || service.operation != tc.operation {
			t.Fatal("lost server request identity or dispatch")
		}
		if tc.method == http.MethodGet && service.req.CSRF != "" {
			t.Fatal("GET consumed CSRF")
		}
		if tc.operation == listRolesOperation {
			if service.page != (cms.RolePage{Limit: 2, Offset: 1}) || len(out) != 2 || out["next_offset"] != nil {
				t.Fatal("role pagination drifted")
			}
			out = out["items"].([]any)[0].(map[string]any)
		}
		assertSafeRoleFields(t, out)
	}
	if service.version != 3 || service.userID != syntheticUserID || service.policy.Grants == nil || len(service.policy.Grants) != 0 {
		t.Fatal("PATCH failed complete grant replacement contract")
	}
}

func assertSafeRoleFields(t *testing.T, out map[string]any) {
	t.Helper()
	if len(out) != 5 {
		t.Fatal("role projection exposed unexpected fields")
	}
	for _, field := range []string{"id", "name", "state", "version", "grants"} {
		if out[field] == nil {
			t.Fatal("role projection omitted field", field)
		}
	}
}

func TestAuthRolesRejectMalformedNestedJSON(t *testing.T) {
	valid := `{"operation":"users.read","scope":"installation","can_delegate":false}`
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"name":"x"}`, `{"grants":[]}`, `{"name":null,"grants":[]}`, `{"name":false,"grants":[]}`, `{"name":"x","grants":null}`, `{"name":"x","grants":{}}`, `{"name":"x","grants":[],"name":"y"}`, `{"name":"x","grants":[],"state":"active"}`, `{"name":"x","grants":[],"actor_id":"x"}`, `{"name":"x","grants":[],"memberships":[]}`, `{"name":"x","grants":[],"expected_version":1}`, `{"name":"x","grants":[]} {}`,
		`{"name":"x","grants":[null]}`, `{"name":"x","grants":[true]}`,
		`{"name":"x","grants":[{"operation":"users.read","scope":"installation"}]}`,
		`{"name":"x","grants":[{"operation":"users.read","operation":"roles.read","scope":"installation","can_delegate":false}]}`,
		`{"name":"x","grants":[{"operation":"users.read","scope":"installation","can_delegate":null}]}`,
		`{"name":"x","grants":[{"operation":"users.read","scope":"installation","can_delegate":"false"}]}`,
		`{"name":"x","grants":[{"operation":"users.read","scope":"installation","can_delegate":false,"may_delegate_content":false}]}`,
		`{"name":"x","grants":[{"operation":"users.read","scope":"installation","can_delegate":false,"field_ids":[]}]}`,
		`{"name":"x","grants":[{"operation":"users.read","scope":"installation","can_delegate":false,"scope,can_delegate":"bad"}]}`,
		`{"name":"x","grants":[{"operation":"content.items.read","scope":"content","can_delegate":false}]}`,
		`{"name":"x","grants":[{"operation":"content.items.read","scope":"content","can_delegate":false,"type_id":"` + syntheticUserID + `","object_scope":"any","field_ids":[null]}]}`,
		`{"name":"x","grants":[{"operation":"content.items.read","scope":"content","can_delegate":false,"type_id":"` + syntheticUserID + `","object_scope":"any","field_ids":["unsafe"]}]}`,
		`{"name":"x","grants":[{"operation":"content.permissions.delegate","scope":"installation","can_delegate":false,"target_scope":"installation","object_ceiling":"any","delegate_operations":["content.items.read"]}]}`,
		`{"name":"x","grants":[` + strings.TrimSuffix(valid, "}") + `,"unknown":true}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, rolesPath, strings.NewReader(body))
			req.Header.Set(contentTypeHeader, jsonMediaType)
			_, code, status := readRoleInput(httptest.NewRecorder(), req, false)
			if code != validationCode || status != 400 {
				t.Fatal("malformed role policy accepted", code, status)
			}
		})
	}
}

func TestAuthRolesConditionalGrantShapesAndVersion(t *testing.T) {
	for _, grant := range []string{
		`{"operation":"users.read","scope":"installation","can_delegate":false}`,
		`{"operation":"content.items.read","scope":"content","can_delegate":false,"type_id":"` + syntheticUserID + `","object_scope":"assigned","field_ids":[]}`,
		`{"operation":"content.items.create","scope":"content","can_delegate":false,"type_id":"` + syntheticUserID + `","object_scope":"type"}`,
		`{"operation":"content.permissions.delegate","scope":"installation","can_delegate":true,"target_scope":"installation","object_ceiling":"any","may_delegate_content":false,"delegate_operations":["content.items.read"]}`,
		`{"operation":"content.permissions.delegate","scope":"installation","can_delegate":false,"target_scope":"type","type_id":"` + syntheticUserID + `","field_ids":[],"object_ceiling":"assigned","may_delegate_content":true,"delegate_operations":["content.items.update"]}`,
	} {
		body := `{"name":"x","grants":[` + grant + `],"expected_version":3}`
		req := httptest.NewRequest(http.MethodPatch, rolePath, strings.NewReader(body))
		req.Header.Set(contentTypeHeader, jsonMediaType)
		input, code, _ := readRoleInput(httptest.NewRecorder(), req, true)
		if code != "" || input.ExpectedVersion != 3 || len(rolePolicy(input).Grants) != 1 {
			t.Fatal("valid conditional role input rejected", body)
		}
	}
	for _, version := range []string{"null", "0", "-1", "1.5", `"1"`, "true", "9223372036854775808"} {
		body := `{"name":"x","grants":[],"expected_version":` + version + `}`
		req := httptest.NewRequest(http.MethodPatch, rolePath, strings.NewReader(body))
		req.Header.Set(contentTypeHeader, jsonMediaType)
		_, _, status := readRoleInput(httptest.NewRecorder(), req, true)
		if status != 400 {
			t.Fatal("invalid expected version accepted", version)
		}
	}
}

func TestAuthRolesBodyLimitAndGrantCap(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"name":"` + strings.Repeat("x", maxRoleBodyBytes) + `","grants":[]}`, 413},
		{`{"name":"x","grants":[` + strings.Repeat(`{"operation":"users.read","scope":"installation","can_delegate":false},`, 64) + `{"operation":"roles.read","scope":"installation","can_delegate":false}]}`, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, rolesPath, strings.NewReader(tc.body))
		req.Header.Set(contentTypeHeader, jsonMediaType)
		_, _, status := readRoleInput(httptest.NewRecorder(), req, false)
		if status != tc.status {
			t.Fatal("role body limit failed", status)
		}
	}
}

func TestAuthRolesBodyDoesNotInheritCredentialLimit(t *testing.T) {
	fields := make([]string, 120)
	for i := range fields {
		fields[i] = fmt.Sprintf("11111111-1111-4111-8111-%012x", i+1)
	}
	input := roleWriteInput{Name: "large-policy", Grants: []roleGrantDTO{{Operation: string(cms.OpContentRead), Scope: roleContentScope, TypeID: syntheticUserID, ObjectScope: roleAnyObjects, FieldIDs: fields}}}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) <= 4096 {
		t.Fatal("large policy fixture is not above credential cap")
	}
	req := httptest.NewRequest(http.MethodPost, rolesPath, strings.NewReader(string(raw)))
	req.Header.Set(contentTypeHeader, jsonMediaType)
	decoded, code, _ := readRoleInput(httptest.NewRecorder(), req, false)
	if code != "" || len(decoded.Grants[0].FieldIDs) != 120 {
		t.Fatal("role body inherited credential or invented field-count limit")
	}
	req = httptest.NewRequest(http.MethodPost, authPrefix+loginPath, strings.NewReader(string(raw)))
	req.Header.Set(contentTypeHeader, jsonMediaType)
	if _, _, status := readCredentials(httptest.NewRecorder(), req, "login,password"); status != 413 {
		t.Fatal("credential limit changed with role editor")
	}
}

func TestAuthRolesSessionOriginQueryAndMethodBoundary(t *testing.T) {
	service := &fakeRoles{}
	server := onboardingTLSServer(t, service)
	for _, kind := range []string{"", preauthKind, loginKind, recoveryKind, enrollmentKind} {
		lifecycleResponse(t, server, roleRequest(t, server, http.MethodGet, rolesPath, "", kind), 401)
	}
	for _, query := range []string{"limit=0", "limit=33", "offset=33", "limit=1&limit=2", "offset=-1", "unknown=x", "limit=1.5"} {
		lifecycleResponse(t, server, roleRequest(t, server, http.MethodGet, rolesPath+"?"+query, "", fullKind), 400)
	}
	for _, id := range []string{"unsafe", "00000000-0000-0000-0000-000000000000", "11111111-1111-4111-8111-11111111111g"} {
		lifecycleResponse(t, server, roleRequest(t, server, http.MethodGet, rolesPath+"/"+id, "", fullKind), 400)
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		path := rolesPath
		if method == http.MethodPatch {
			path += "/" + syntheticUserID
		}
		for _, header := range []string{originHeader, testCSRFHeader} {
			req := roleRequest(t, server, method, path, `{"name":"x","grants":[],"expected_version":1}`, fullKind)
			req.Header.Del(header)
			lifecycleResponse(t, server, req, 403)
		}
	}
	for path, allow := range map[string]string{rolesPath: "GET, POST", rolesPath + "/" + syntheticUserID: "GET, PATCH"} {
		req := roleRequest(t, server, http.MethodDelete, path, "{}", fullKind)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 405 || res.Header.Get("Allow") != allow {
			t.Fatal("role method allowlist drifted", res.StatusCode, res.Header.Get("Allow"))
		}
	}
	if service.calls != 0 {
		t.Fatal("unsafe role request reached service")
	}
}

func TestAuthRolesSafeServiceErrors(t *testing.T) {
	service := &fakeRoles{}
	server := onboardingTLSServer(t, service)
	for err, expected := range map[error]struct {
		status int
		code   string
	}{cms.ErrPermissionDenied: {403, "permission_denied"}, cms.ErrStepUpRequired: {403, "step_up_required"}, cms.ErrLastAdminRequired: {409, "last_admin_required"}, cms.ErrValidation: {400, "validation_failed"}, cms.ErrSecurityUnavailable: {503, "security_unavailable"}} {
		service.commandErr = err
		out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPatch, rolesPath+"/"+syntheticUserID, `{"name":"x","grants":[],"expected_version":3}`, fullKind), expected.status)
		if len(out) != 3 || out["code"] != expected.code {
			t.Fatal("unsafe role problem projection")
		}
	}
}

func TestAuthRolesAuthorityFalseRoundTrip(t *testing.T) {
	role := safeRole(cms.Role{ID: syntheticUserID, Name: "authority", State: lifecycleFixtureState, Version: 1, Grants: []cms.RoleGrant{{Operation: "content.permissions.delegate", Scope: roleInstallationScope, TargetScope: roleInstallationScope, ObjectCeiling: "any", DelegateOperations: []string{"content.items.read"}}}})
	raw, err := json.Marshal(role.Grants[0])
	if err != nil || !strings.Contains(string(raw), `"may_delegate_content":false`) || !roleGrants(append(append([]byte{'['}, raw...), ']')) {
		t.Fatal("false delegation flag omitted or response no longer accepted", string(raw))
	}
}
