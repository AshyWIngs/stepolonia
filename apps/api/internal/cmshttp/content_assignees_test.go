package cmshttp

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/cms"
)

const assigneesTestPath = "/api/v1/admin/content/article/" + exampleUUID + "/assignees"
const clearAssigneesBody = `{"expected_version":"7","assignee_ids":[]}`
const assigneesTestField = "assignee_ids"
const assigneesVersionTestField = "expected_version"

type fakeContentAssignees struct {
	fakeAuth
	target routeTarget
	input  replaceContentAssigneesInput
}

func (s *fakeContentAssignees) ReplaceContentAssignees(_ context.Context, req cms.AuthRequest, key, id string, version int64, assigneeIDs []string) (cms.ContentAssignees, error) {
	s.calls++
	s.req = req
	s.target = routeTarget{id: id, typeKey: key}
	s.input = replaceContentAssigneesInput{ExpectedVersion: version, AssigneeIDs: assigneeIDs}
	return cms.ContentAssignees{ItemID: id, Version: version + 1, AssigneeIDs: assigneeIDs}, nil
}

func TestContentAssigneesDispatch(t *testing.T) {
	service := &fakeContentAssignees{}
	server := newAdminTLSServer(t, service)
	req := roleRequest(t, server, http.MethodPut, assigneesTestPath, clearAssigneesBody, fullKind)
	req.Header.Set(requestIDHeader, "untrusted-request-id")
	out := lifecycleResponse(t, server, req, http.StatusOK)
	want := map[string]any{"item_id": exampleUUID, "version": "8", assigneesTestField: []any{}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("unexpected committed assignment projection: %v", out)
	}
	wantTarget := routeTarget{id: exampleUUID, typeKey: "article"}
	wantInput := replaceContentAssigneesInput{ExpectedVersion: 7, AssigneeIDs: []string{}}
	if service.calls != 1 || service.target != wantTarget || !reflect.DeepEqual(service.input, wantInput) {
		t.Fatalf("incorrect target or request forwarded: %v %v", service.target, service.input)
	}
	if service.req.Token != testToken || service.req.CSRF != testToken || !service.req.ClientIP.IsLoopback() || service.req.RequestID == "" || service.req.RequestID == "untrusted-request-id" {
		t.Fatal("incorrect verified transport context")
	}
}

func TestContentAssigneesRejectsOutOfContractRequests(t *testing.T) {
	service := &fakeContentAssignees{}
	server := newAdminTLSServer(t, service)
	for _, body := range []string{
		`{"assignee_ids":[]}`,
		`{"expected_version":"7"}`,
		`{"expected_version":7,"assignee_ids":[]}`,
		`{"expected_version":"07","assignee_ids":[]}`,
		`{"expected_version":"0","assignee_ids":[]}`,
		`{"expected_version":"9223372036854775808","assignee_ids":[]}`,
		`{"expected_version":"7","assignee_ids":null}`,
		`{"expected_version":"7","assignee_ids":[null]}`,
		`{"expected_version":"7","assignee_ids":[],"assignee_ids":[]}`,
		`{"expected_version":"7","assignee_ids":[],"actor_id":"forged"}`,
		clearAssigneesBody + `{}`,
	} {
		out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPut, assigneesTestPath, body, fullKind), http.StatusBadRequest)
		if out["code"] != validationCode {
			t.Fatalf("unexpected problem for %s: %v", body, out)
		}
	}
	for _, tc := range []struct {
		method, path, kind, body string
		status                   int
	}{
		{http.MethodPut, assigneesTestPath, loginKind, clearAssigneesBody, http.StatusUnauthorized},
		{http.MethodPatch, assigneesTestPath, fullKind, clearAssigneesBody, http.StatusMethodNotAllowed},
		{http.MethodPut, assigneesTestPath + "?page=1", fullKind, clearAssigneesBody, http.StatusBadRequest},
		{http.MethodPut, assigneesTestPath, fullKind, strings.Repeat(" ", maxCredentialBodyBytes+1), http.StatusRequestEntityTooLarge},
	} {
		lifecycleResponse(t, server, roleRequest(t, server, tc.method, tc.path, tc.body, tc.kind), tc.status)
	}
	if service.calls != 0 {
		t.Fatal("invalid assignment request reached the service")
	}
}

func TestContentAssigneesOpenAPI(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	op := doc.Paths["/api/v1/admin/content/{type_key}/{item_id}/assignees"]["put"]
	if op.OperationID != "contentReplaceAssignees" || !slices.Equal(op.Tags, []string{"content-assignments"}) || len(op.Security) != 1 {
		t.Fatalf("incorrect assignment operation: %+v", op)
	}
	if _, ok := op.Security[0]["__Host-sp_session"]; !ok {
		t.Fatal("replacement must document a full session")
	}
	for _, name := range []string{"type_key", "item_id"} {
		if !slices.ContainsFunc(op.Parameters, func(p *huma.Param) bool { return p.Name == name && p.In == "path" && p.Required }) {
			t.Fatalf("missing required path parameter %s", name)
		}
	}
	if _, ok := op.Responses["200"].Headers["Set-Cookie"]; ok {
		t.Fatal("assignment success does not rotate session cookies")
	}
}

func TestContentAssigneesRequestSchema(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	req := doc.Components.Schemas["contentReplaceAssigneesRequest"]
	if req == nil || req.AdditionalProperties != false || !slices.Equal(req.Required, []string{assigneesVersionTestField, assigneesTestField}) {
		t.Fatal("assignment schema must require the version and complete set")
	}
	if req.Properties[assigneesVersionTestField].Type != "string" || doc.Components.Schemas["ContentAssignees"].Properties["version"].Type != "string" {
		t.Fatal("item versions must preserve int64 precision as strings")
	}
	ids := req.Properties[assigneesTestField]
	if ids.Type != "array" || ids.Items.Format != "uuid" || !ids.UniqueItems || ids.MaxItems == nil || *ids.MaxItems != 100 {
		t.Fatal("incorrect bounded UUID array")
	}
}
