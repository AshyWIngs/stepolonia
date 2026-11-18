package adminhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const draftTestTypeKey = "article"
const draftTestPath = "/api/v1/admin/content/" + draftTestTypeKey + "/" + exampleUUID
const minimumDraftBody = `{"expected_version":"2","schema_no":"1"}`
const draftLocalChanges = `{"expected_version":"2","schema_no":"1","locales":{"en":{"values":{"title":{"kind":"string","value":"Changed"}},"unset":["note"]}}}`

type fakeDraftEditor struct {
	calls   int
	req     identity.AuthRequest
	target  routeTarget
	version int64
	patch   content.DraftPatch
	state   content.DraftState
	err     error
}

func (f *fakeDraftEditor) AdoptSchemaForType(_ context.Context, req identity.AuthRequest, key, id string, version, _ int64) (content.DraftState, error) {
	f.calls++
	f.req, f.target, f.version = req, routeTarget{typeKey: key, id: id}, version
	return f.state, f.err
}

func (f *fakeDraftEditor) UnpublishForType(ctx context.Context, req identity.AuthRequest, key, id string, version, schema int64) (content.DraftState, error) {
	return f.AdoptSchemaForType(ctx, req, key, id, version, schema)
}

func (f *fakeDraftEditor) CreateDraftForType(_ context.Context, req identity.AuthRequest, key string, schemaNo int64) (content.DraftState, error) {
	f.calls++
	f.req, f.target, f.version = req, routeTarget{typeKey: key}, schemaNo
	return f.state, f.err
}

func (f *fakeDraftEditor) SaveDraftForType(_ context.Context, req identity.AuthRequest, key, id string, expected int64, patch content.DraftPatch) (content.DraftState, error) {
	f.calls++
	f.req, f.target, f.version, f.patch = req, routeTarget{typeKey: key, id: id}, expected, patch
	return f.state, f.err
}

func (f *fakeDraftEditor) ReadDraftForType(_ context.Context, req identity.AuthRequest, key, id string) (content.DraftState, error) {
	f.calls++
	f.req, f.target = req, routeTarget{typeKey: key, id: id}
	return f.state, f.err
}

func (f *fakeDraftEditor) ListDraftsForType(_ context.Context, req identity.AuthRequest, key string, _, _ int) (identity.DraftPage, error) {
	f.calls++
	f.req, f.target = req, routeTarget{typeKey: key}
	return identity.DraftPage{Items: []identity.DraftSummary{}}, f.err
}

func (f *fakeDraftEditor) ListReadableCollections(_ context.Context, _ identity.AuthRequest, _, _ int) (identity.ReadableCollectionPage, error) {
	return identity.ReadableCollectionPage{}, f.err
}

func (f *fakeDraftEditor) ReadDraftMetadata(_ context.Context, _ identity.AuthRequest, _, _ string) (content.DraftMetadata, error) {
	return content.DraftMetadata{}, f.err
}

func newDraftTLSServer(t *testing.T, editor DraftEditor) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	auth, err := NewHandlerWithDraftEditor(Config{CanonicalOrigin: origin}, &fakeAuth{}, editor)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = auth
	server.StartTLS()
	return server
}

const draftTestTitle = "title"

func TestDraftSaveForwardsVerifiedRequestAndReturnsProjection(t *testing.T) {
	state := content.DraftState{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 3, SharedSeq: 5,
		SharedReadySeq: sql.NullInt64{Int64: 5, Valid: true}, Locales: map[string]content.LocaleDraft{
			"en": {Values: content.ScalarValues{draftTestTitle: json.RawMessage(`"Changed"`)}, LocalSeq: 7},
		}}
	editor := &fakeDraftEditor{state: state}
	server := newDraftTLSServer(t, editor)
	req := roleRequest(t, server, http.MethodPatch, draftTestPath, draftLocalChanges, fullKind)
	req.Header.Set(requestIDHeader, "forged")
	out := lifecycleResponse(t, server, req, http.StatusOK)
	assertDraftProjection(t, out)
	locale := editor.patch.Locales["en"]
	if editor.calls != 1 || editor.target != (routeTarget{id: exampleUUID, typeKey: draftTestTypeKey}) || editor.version != 2 ||
		editor.patch.SchemaNo != 1 || string(locale.Values[draftTestTitle].Value) != `"Changed"` || !reflect.DeepEqual(locale.Unset, []string{"note"}) {
		t.Fatalf("wrong command: %+v", editor)
	}
	if editor.req.Token != testToken || editor.req.CSRF != testToken || !editor.req.ClientIP.IsLoopback() || editor.req.RequestID == "" || editor.req.RequestID == "forged" {
		t.Fatal("request lost its verified boundary")
	}
}

func assertDraftProjection(t *testing.T, out map[string]any) {
	t.Helper()
	var want map[string]any
	const example = `{"item_id":%q,"type_id":%q,"schema_no":"1","version":"3","shared":{},"shared_seq":"5","shared_ready_seq":"5","locales":{"en":{"values":{"title":"Changed"},"local_seq":"7","ready":null}},"relations":[]}`
	if err := json.Unmarshal(fmt.Appendf(nil, example, exampleUUID, exampleUUID), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("wrong saved draft projection: %v", out)
	}
}

func TestDraftSaveWriteOnlyResponseDoesNotSerializeDraftState(t *testing.T) {
	editor := &fakeDraftEditor{state: content.DraftState{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 3}}
	server := newDraftTLSServer(t, editor)
	out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPatch, draftTestPath, minimumDraftBody, fullKind), http.StatusOK)
	if len(out) != 4 || !matchesDraftReceipt(t, out) {
		t.Fatalf("write-only response returned more than identity and version: %v", out)
	}
}

func matchesDraftReceipt(t *testing.T, out map[string]any) bool {
	t.Helper()
	var want map[string]any
	const receipt = `{"item_id":%q,"type_id":%q,"schema_no":"1","version":"3"}`
	if err := json.Unmarshal(fmt.Appendf(nil, receipt, exampleUUID, exampleUUID), &want); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(out, want)
}

func TestDraftSaveRejectsMalformedPatches(t *testing.T) {
	editor := &fakeDraftEditor{}
	server := newDraftTLSServer(t, editor)
	for _, body := range []string{
		`{"expected_version":"2"}`,
		`{"expected_version":2,"schema_no":"1"}`,
		`{"expected_version":"2","schema_no":"9223372036854775808"}`,
		`{"expected_version":"2","schema_no":"01"}`,
		`{"expected_version":"2","schema_no":"1","locales":null}`,
		`{"expected_version":"2","schema_no":"1","relations":[]}`,
		`{"expected_version":"2","schema_no":"1","locales":{"en":{"values":{},"Values":{}}}}`,
		`{"expected_version":"2","schema_no":"1","shared":{"x":{"kind":"string","kind":"text","value":"a"}}}`,
		`{"expected_version":"2","schema_no":"1","shared":{"x":{"kind":"text","value":{}}}}`,
		`{"expected_version":"2","schema_no":"1","unset_shared":[{}]}`,
		`{"expected_version":"2","schema_no":"1","shared":{"x":{"kind":"text","value":null}}}`,
		minimumDraftBody + `{}`,
	} {
		out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPatch, draftTestPath, body, fullKind), http.StatusBadRequest)
		if out["code"] != validationCode {
			t.Fatalf("bad body returned wrong error: %v", out)
		}
	}
	if editor.calls != 0 {
		t.Fatal("malformed draft reached the database boundary")
	}
}

func TestDraftSaveEnforcesPrivateBoundary(t *testing.T) {
	editor := &fakeDraftEditor{}
	server := newDraftTLSServer(t, editor)
	for _, tc := range []struct {
		method, kind, body string
		status             int
	}{
		{http.MethodPatch, loginKind, minimumDraftBody, http.StatusUnauthorized},
		{http.MethodPut, fullKind, minimumDraftBody, http.StatusMethodNotAllowed},
		{http.MethodPatch, fullKind, strings.Repeat(" ", maxDraftBodyBytes+1), http.StatusRequestEntityTooLarge},
	} {
		lifecycleResponse(t, server, roleRequest(t, server, tc.method, draftTestPath, tc.body, tc.kind), tc.status)
	}
	for _, header := range []string{originHeader, testCSRFHeader} {
		req := roleRequest(t, server, http.MethodPatch, draftTestPath, minimumDraftBody, fullKind)
		req.Header.Del(header)
		lifecycleResponse(t, server, req, http.StatusForbidden)
	}
	disabled := newAdminTLSServer(t, &fakeAuth{})
	lifecycleResponse(t, disabled, roleRequest(t, disabled, http.MethodPatch, draftTestPath, minimumDraftBody, fullKind), http.StatusNotFound)
	if editor.calls != 0 {
		t.Fatal("invalid transport reached the draft editor")
	}
}

func TestDraftSavePreservesSchemaAndVersionConflicts(t *testing.T) {
	for _, code := range []string{schemaConflictCode, versionConflictCode} {
		editor := &fakeDraftEditor{err: &content.Error{Kind: content.Conflict, Code: code}}
		server := newDraftTLSServer(t, editor)
		out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPatch, draftTestPath, minimumDraftBody, fullKind), http.StatusConflict)
		if out["code"] != code {
			t.Fatalf("wrong conflict code: %v", out)
		}
	}
}

func TestDraftSaveOpenAPIUsesTypedPathAndFullSession(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	op := doc.Paths["/api/v1/admin/content/{type_key}/{item_id}"]["patch"]
	if op.OperationID != "contentSaveDraft" || len(op.Security) != 1 {
		t.Fatalf("incorrect draft operation: %+v", op)
	}
	if _, ok := op.Security[0]["__Host-sp_session"]; !ok {
		t.Fatal("draft save must document a full session")
	}
	if _, ok := op.Responses["200"].Headers["Set-Cookie"]; ok {
		t.Fatal("draft save must not rotate session cookies")
	}
}

func TestDraftReadUsesFullSessionAndSameOriginMetadata(t *testing.T) {
	editor := &fakeDraftEditor{state: content.DraftState{
		ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 3, SharedSeq: 1,
		Shared: content.ScalarValues{draftTestTitle: json.RawMessage(`"Readable draft"`)},
	}}
	server := newDraftTLSServer(t, editor)
	req := lifecycleGET(t, server, draftTestPath, fullKind)
	req.Header.Set(requestIDHeader, "forged-read")
	out := lifecycleResponse(t, server, req, http.StatusOK)
	if editor.calls != 1 || editor.target != (routeTarget{id: exampleUUID, typeKey: draftTestTypeKey}) ||
		editor.req.Token != testToken || editor.req.CSRF != "" || !editor.req.ClientIP.IsLoopback() ||
		editor.req.RequestID == "" || editor.req.RequestID == "forged-read" {
		t.Fatal("draft read lost its verified boundary")
	}
	if out["shared"].(map[string]any)[draftTestTitle] != "Readable draft" {
		t.Fatalf("draft read omitted its projection: %v", out)
	}
}

func TestDraftReadRejectsInvalidBoundary(t *testing.T) {
	editor := &fakeDraftEditor{}
	server := newDraftTLSServer(t, editor)
	lifecycleResponse(t, server, lifecycleGET(t, server, draftTestPath, loginKind), http.StatusUnauthorized)
	lifecycleResponse(t, server, lifecycleGET(t, server, draftTestPath+"?limit=1", fullKind), http.StatusBadRequest)
	crossOrigin := lifecycleGET(t, server, draftTestPath, fullKind)
	crossOrigin.Header.Del(refererHeader)
	lifecycleResponse(t, server, crossOrigin, http.StatusForbidden)
	withBody := lifecycleGET(t, server, draftTestPath, fullKind)
	withBody.Body = http.NoBody
	withBody.TransferEncoding = []string{"chunked"}
	lifecycleResponse(t, server, withBody, http.StatusBadRequest)
	disabled := newAdminTLSServer(t, &fakeAuth{})
	lifecycleResponse(t, disabled, lifecycleGET(t, disabled, draftTestPath, fullKind), http.StatusNotFound)
	if editor.calls != 0 {
		t.Fatal("invalid read reached the draft editor")
	}
}

func TestDraftReadOpenAPIDescribesOnlyReadSemantics(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	op := doc.Paths[contentDraftPath]["get"]
	if op.OperationID != "contentGetDraft" || len(op.Security) != 1 || op.RequestBody != nil {
		t.Fatalf("incorrect draft read operation: %+v", op)
	}
	if _, ok := op.Security[0]["__Host-sp_session"]; !ok {
		t.Fatal("draft read must document a full session")
	}
	for _, param := range op.Parameters {
		if param.Name == testCSRFHeader || param.In != openAPIPathParameter && param.In != "header" {
			t.Fatalf("unexpected parameter on draft read: %+v", param)
		}
	}
	if len(op.Parameters) != 3 || op.Responses["200"].Headers["Set-Cookie"] != nil {
		t.Fatalf("incorrect draft read path or success headers: %+v", op)
	}
}
