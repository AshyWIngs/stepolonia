package adminhttp

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

const userRolesTestPath = "/api/v1/admin/users/" + exampleUUID + "/roles"
const clearUserRolesBody = `{"expected_version":7,"role_ids":[]}`

type fakeUserRoles struct {
	fakeAuth
	id      string
	version int64
	roleIDs []string
}

func (s *fakeUserRoles) ReplaceUserRoles(_ context.Context, req identity.AuthRequest, id string, version int64, roleIDs []string) (identity.User, error) {
	s.calls++
	s.req, s.id, s.version, s.roleIDs = req, id, version, roleIDs
	return identity.User{ID: id, Login: lifecycleFixtureLogin, State: lifecycleFixtureState, Version: version + 1}, nil
}

func TestUserRolesDispatchAndSafeProjection(t *testing.T) {
	service := &fakeUserRoles{}
	server := newAdminTLSServer(t, service)
	for _, tc := range []struct {
		body string
		ids  []string
	}{
		{clearUserRolesBody, []string{}},
		{`{"expected_version":7,"role_ids":["` + exampleUUID + `"]}`, []string{exampleUUID}},
	} {
		req := roleRequest(t, server, http.MethodPut, userRolesTestPath, tc.body, fullKind)
		req.Header.Set(requestIDHeader, "incoming-header")
		user := lifecycleResponse(t, server, req, http.StatusOK)
		assertSafeUserFields(t, user)
		if service.id != exampleUUID || service.version != 7 || service.roleIDs == nil || !slices.Equal(service.roleIDs, tc.ids) {
			t.Fatal("lost target, version or complete role set")
		}
		if service.req.Token != testToken || service.req.CSRF != testToken || !service.req.ClientIP.IsLoopback() || service.req.RequestID == "" || service.req.RequestID == "incoming-header" {
			t.Fatal("lost verified request context")
		}
		if user["version"] != float64(8) || user["state"] != lifecycleFixtureState {
			t.Fatal("unexpected safe account projection")
		}
	}
}

func TestUserRolesRejectsInvalidBodiesBeforeService(t *testing.T) {
	service := &fakeUserRoles{}
	server := newAdminTLSServer(t, service)
	for _, body := range []string{
		`{"role_ids":[]}`,
		`{"expected_version":0,"role_ids":[]}`,
		`{"expected_version":"1","role_ids":[]}`,
		`{"expected_version":79}`,
		`{"expected_version":1,"role_ids":null}`,
		`{"expected_version":1,"role_ids":{}}`,
		`{"expected_version":1,"role_ids":[null]}`,
		`{"expected_version":1,"role_ids":[1]}`,
		`{"expected_version":1,"role_ids":[],"role_ids":[]}`,
		`{"expected_version":1,"role_ids":[],"expected_version":2}`,
		`{"expected_version":1,"role_ids":[],"actor_id":"manager"}`,
		clearUserRolesBody + `{}`,
	} {
		out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPut, userRolesTestPath, body, fullKind), http.StatusBadRequest)
		if out["code"] != "validation_failed" {
			t.Fatalf("unexpected error for %s: %v", body, out)
		}
	}
	req := roleRequest(t, server, http.MethodPut, userRolesTestPath, strings.Repeat(" ", 4097), fullKind)
	lifecycleResponse(t, server, req, http.StatusRequestEntityTooLarge)
	if service.calls != 0 {
		t.Fatal("invalid body reached service")
	}
}

func TestUserRolesRequiresSameOriginFullSessionAndCSRF(t *testing.T) {
	service := &fakeUserRoles{}
	server := newAdminTLSServer(t, service)
	for _, kind := range []string{"", preauthKind, loginKind, recoveryKind, enrollmentKind} {
		req := roleRequest(t, server, http.MethodPut, userRolesTestPath, clearUserRolesBody, kind)
		lifecycleResponse(t, server, req, http.StatusUnauthorized)
	}
	for header, code := range map[string]string{originHeader: "origin_denied", testCSRFHeader: "csrf_failed"} {
		req := roleRequest(t, server, http.MethodPut, userRolesTestPath, clearUserRolesBody, fullKind)
		req.Header.Del(header)
		out := lifecycleResponse(t, server, req, http.StatusForbidden)
		if out["code"] != code {
			t.Fatalf("unexpected response without %s: %v", header, out)
		}
	}
	req := roleRequest(t, server, http.MethodPatch, userRolesTestPath, clearUserRolesBody, fullKind)
	lifecycleResponse(t, server, req, http.StatusMethodNotAllowed)
	if service.calls != 0 {
		t.Fatal("request outside the transport boundary reached service")
	}
}

func TestUserRolesOpenAPIBoundsAndSecurity(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	op := doc.Paths["/api/v1/admin/users/{id}/roles"]["put"]
	if op.OperationID != "usersReplaceRoles" || !slices.Equal(op.Tags, []string{"users"}) || len(op.Security) != 1 {
		t.Fatal("missing private user roles operation")
	}
	if _, ok := op.Security[0]["__Host-sp_session"]; !ok {
		t.Fatal("PUT operation requires a full session")
	}
	schema := doc.Components.Schemas["usersReplaceRolesRequest"]
	roles := schema.Properties["role_ids"]
	if roles.Type != "array" || roles.Items.Format != "uuid" || !roles.UniqueItems || roles.Nullable || roles.MaxItems == nil || *roles.MaxItems != 16 {
		t.Fatal("role_ids does not document the bounded UUID set")
	}
	if !slices.Equal(schema.Required, []string{"expected_version", "role_ids"}) || schema.AdditionalProperties != false {
		t.Fatal("membership request schema does not enforce the complete replacement")
	}
	for _, code := range []string{"200", "400", "401", "403", "404", "405", "409", "413", "429", "503"} {
		if _, ok := op.Responses[code]; !ok {
			t.Fatalf("missing status %s", code)
		}
	}
}
