package adminhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/content"
)

const (
	createDraftTestPath = "/api/v1/admin/content/article"
	createDraftTestBody = `{"schema_no":"1"}`
)

const forgedCreateRequestID = "client-supplied-id"
const invalidDraftInputCode = "validation_failed"

func expectedCreateDraftReceipt(t *testing.T) map[string]any {
	t.Helper()
	var receipt map[string]any
	const example = `{"item_id":%q,"type_id":%q,"schema_no":"1","version":"1"}`
	if err := json.Unmarshal(fmt.Appendf(nil, example, exampleUUID, exampleUUID), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestCreateDraftReturnsReceiptAndLocation(t *testing.T) {
	editor := &fakeDraftEditor{state: content.DraftState{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 1, SharedSeq: 1}}
	server := newDraftTLSServer(t, editor)
	req := roleRequest(t, server, http.MethodPost, createDraftTestPath, createDraftTestBody, fullKind)
	req.Header.Set(requestIDHeader, forgedCreateRequestID)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	assertPrivateNoStore(t, res)
	if res.StatusCode != http.StatusCreated || res.Header.Get("Location") != createDraftTestPath+"/"+exampleUUID || len(res.Cookies()) != 0 {
		t.Fatalf("wrong status or headers: %d %v", res.StatusCode, res.Header)
	}
	var out map[string]any
	want := expectedCreateDraftReceipt(t)
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || !reflect.DeepEqual(out, want) {
		t.Fatalf("wrong draft creation receipt: %v (%v)", out, err)
	}
	assertCreateDraftContext(t, editor, res.Header.Get(requestIDHeader))
}

func assertCreateDraftContext(t *testing.T, editor *fakeDraftEditor, responseRequestID string) {
	t.Helper()
	if editor.calls != 1 || editor.target.typeKey != draftTestTypeKey || editor.version != 1 {
		t.Fatalf("wrong create draft command: %+v", editor.target)
	}
	if editor.req.Token != testToken || editor.req.CSRF != testToken || !editor.req.ClientIP.IsLoopback() ||
		editor.req.RequestID == "" || editor.req.RequestID == forgedCreateRequestID || editor.req.RequestID != responseRequestID {
		t.Fatal("draft command lost its authenticated transport context")
	}
}

func TestCreateDraftRejectsInvalidInput(t *testing.T) {
	editor := &fakeDraftEditor{}
	server := newDraftTLSServer(t, editor)
	for _, body := range []string{
		`{}`, `[]`, "null\n", `{"schema_no":null}`, `{"schema_no":1}`, `{"schema_no":"+1"}`, `{"schema_no":"01"}`,
		`{"schema_no":"0"}`, `{"schema_no":"9223372036854775808"}`, `{"Schema_No":"1"}`,
		`{"schema_no":"1","schema_no":"2"}`, `{"schema_no":"1","shared":{}}`, createDraftTestBody + `{}`,
	} {
		out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPost, createDraftTestPath, body, fullKind), http.StatusBadRequest)
		if out["code"] != invalidDraftInputCode {
			t.Fatalf("wrong failure code for %q: %v", body, out)
		}
	}
	oversized := createDraftTestBody + strings.Repeat(" ", 4096)
	out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPost, createDraftTestPath, oversized, fullKind), http.StatusRequestEntityTooLarge)
	if editor.calls != 0 || out["code"] != "body_too_large" {
		t.Fatalf("invalid input reached draft editor or returned wrong error: %v", out)
	}
}

func TestCreateDraftRequiresEnabledEditorAndWriteBoundary(t *testing.T) {
	editor := &fakeDraftEditor{}
	disabledServer := newDraftTLSServer(t, nil)
	lifecycleResponse(t, disabledServer, roleRequest(t, disabledServer, http.MethodPost, createDraftTestPath, createDraftTestBody, fullKind), http.StatusNotFound)

	server := newDraftTLSServer(t, editor)
	req := roleRequest(t, server, http.MethodPost, createDraftTestPath, createDraftTestBody, fullKind)
	req.Header.Del(originHeader)
	lifecycleResponse(t, server, req, http.StatusForbidden)
	req = roleRequest(t, server, http.MethodPost, createDraftTestPath, createDraftTestBody, fullKind)
	req.Header.Del(testCSRFHeader)
	lifecycleResponse(t, server, req, http.StatusForbidden)
	req = roleRequest(t, server, http.MethodPost, createDraftTestPath, createDraftTestBody, preauthKind)
	lifecycleResponse(t, server, req, http.StatusUnauthorized)
	req = roleRequest(t, server, http.MethodPut, createDraftTestPath, createDraftTestBody, fullKind)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("allow") != "GET, POST" || editor.calls != 0 {
		t.Fatalf("wrong method rejection or write dispatched: %d %v", res.StatusCode, res.Header)
	}
}

func TestCreateDraftPrivateOpenAPI(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	op := doc.Paths["/api/v1/admin/content/{type_key}"]["post"]
	if op.OperationID != "contentCreateDraft" || len(op.Security) != 1 || op.RequestBody == nil {
		t.Fatalf("missing secured create operation: %+v", op)
	}
	checkCreateDraftOpenAPIParameters(t, op)
	checkCreateDraftOpenAPIReceipt(t, doc, op)
	if conflict := op.Responses["409"].Content["application/problem+json"].Examples; len(conflict) != 1 || conflict["schema_conflict"].Value == nil {
		t.Fatalf("wrong conflict documentation: %v", conflict)
	}
}

func checkCreateDraftOpenAPIParameters(t *testing.T, op privateOperation) {
	t.Helper()
	var pathParameters []string
	for _, parameter := range op.Parameters {
		if parameter.In == "path" {
			pathParameters = append(pathParameters, parameter.Name)
			if !parameter.Required {
				t.Fatalf("optional path parameter: %s", parameter.Name)
			}
		}
	}
	if !slices.Equal(pathParameters, []string{"type_key"}) {
		t.Fatalf("wrong create operation parameters: %v", pathParameters)
	}
}

func checkCreateDraftOpenAPIReceipt(t *testing.T, doc privateDocument, op privateOperation) {
	t.Helper()
	success := op.Responses["201"]
	if success.Headers["Location"] == nil || success.Headers["Set-Cookie"] != nil {
		t.Fatalf("wrong create operation headers: %v", success.Headers)
	}
	receipt := privateRegistry(doc).SchemaFromRef(success.Content["application/json"].Schema.Ref)
	want := expectedCreateDraftReceipt(t)
	if receipt == nil || receipt.AdditionalProperties != false || len(receipt.Properties) != len(want) || len(receipt.Required) != len(want) {
		t.Fatalf("wrong create receipt schema: %+v", receipt)
	}
	for field := range want {
		if receipt.Properties[field] == nil || !slices.Contains(receipt.Required, field) {
			t.Fatalf("missing required create receipt field: %s", field)
		}
	}
}
