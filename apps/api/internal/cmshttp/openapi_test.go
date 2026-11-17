package cmshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type privateDocument struct {
	Paths      map[string]map[string]privateOperation
	Components struct{ Schemas map[string]*huma.Schema }
}

type privateOperation struct {
	OperationID string `json:"operationId"`
	Description string
	RequestBody *struct{ Content map[string]privateMedia } `json:"requestBody"`
	Responses   map[string]struct{ Content map[string]privateMedia }
}

type privateMedia struct {
	Schema struct {
		Ref string `json:"$ref"`
	}
	Example  any
	Examples map[string]struct{ Value any }
}

func readPrivateDocument(t *testing.T) (privateDocument, []byte) {
	t.Helper()
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc privateDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, raw
}

func TestPrivateOpenAPICoversDispatchRegistry(t *testing.T) {
	doc, raw := readPrivateDocument(t)
	repeated, err := OpenAPIJSON()
	if err != nil || !bytes.Equal(raw, repeated) {
		t.Fatal("private specification is not deterministic", err)
	}
	if len(doc.Paths) != len(routes) {
		t.Fatal("documented paths differ from dispatch registry")
	}
	ids := map[string]bool{}
	for path, rt := range routes {
		op := checkRegistryOperation(t, doc, path, rt)
		if ids[op.OperationID] {
			t.Fatalf("duplicate operation ID: %s", op.OperationID)
		}
		ids[op.OperationID] = true
	}
	for _, forbidden := range []string{"EncryptionKey", "DATABASE_URL", "password_hash", "ciphertext", "token_digest"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("private implementation detail leaked: %s", forbidden)
		}
	}
}

func checkRegistryOperation(t *testing.T, doc privateDocument, path string, rt route) privateOperation {
	t.Helper()
	operations := doc.Paths[authPrefix+path]
	op, ok := operations[strings.ToLower(rt.method)]
	if !ok || len(operations) != 1 || op.OperationID == "" || len(op.Description) < 100 {
		t.Fatalf("missing or incomplete operation %s: %+v", path, op)
	}
	if rt.method == http.MethodGet {
		if op.RequestBody != nil {
			t.Fatal("csrf GET must not declare a body")
		}
		return op
	}
	if op.RequestBody == nil {
		t.Fatalf("missing request body %s", path)
	}
	checkCredentialProperties(t, doc, rt, op.RequestBody.Content[jsonMediaType])
	return op
}

func checkCredentialProperties(t *testing.T, doc privateDocument, rt route, body privateMedia) {
	t.Helper()
	schema := doc.Components.Schemas[strings.TrimPrefix(body.Schema.Ref, schemaPrefix)]
	if schema == nil || schema.AdditionalProperties != false {
		t.Fatalf("missing closed credential schema %s", rt.operationID)
	}
	exampleJSON, err := json.Marshal(body.Example)
	if err != nil || !allowedObject(exampleJSON, rt.fields) {
		t.Fatalf("example rejected by actual JSON boundary: %s", rt.operationID)
	}
	example, ok := body.Example.(map[string]any)
	if !ok {
		t.Fatal("credential example is not an object")
	}
	fields := []string{}
	if rt.fields != "" {
		fields = strings.Split(rt.fields, ",")
	}
	for _, field := range fields {
		property := schema.Properties[field]
		if property == nil || len(property.Description) < 30 || example[field] == "" || example[field] == nil {
			t.Fatalf("missing credential description/example for %s %s", rt.operationID, field)
		}
	}
	if len(schema.Properties) != len(fields) || len(example) != len(fields) {
		t.Fatalf("credential fields drifted from dispatch registry: %s", rt.operationID)
	}
}

func TestPrivateOpenAPIExamplesValidateAgainstSchemas(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	for name, schema := range doc.Components.Schemas {
		schema.PrecomputeMessages()
		registry.Map()[name] = schema
	}
	for name, schema := range doc.Components.Schemas {
		if len(schema.Description) < 40 || len(schema.Examples) == 0 {
			t.Fatalf("schema lacks meaningful metadata: %s", name)
		}
		for _, example := range schema.Examples {
			validatePrivateExample(t, registry, name, example)
		}
	}
	for path, methods := range doc.Paths {
		for _, op := range methods {
			checkResponseExamples(t, registry, path, op)
		}
	}
}

func validatePrivateExample(t *testing.T, registry huma.Registry, name string, value any) {
	t.Helper()
	schema := registry.Map()[name]
	if schema == nil {
		t.Fatalf("missing schema %s", name)
	}
	var result huma.ValidateResult
	huma.Validate(registry, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	if len(result.Errors) != 0 {
		t.Fatalf("%s example invalid: %v", name, result.Errors)
	}
}

func checkResponseExamples(t *testing.T, registry huma.Registry, path string, op privateOperation) {
	t.Helper()
	for status, response := range op.Responses {
		if status == "204" {
			if len(response.Content) != 0 {
				t.Fatalf("204 declares content: %s", path)
			}
			continue
		}
		for _, media := range response.Content {
			checkMediaExamples(t, registry, path, status, media)
		}
	}
}

func checkMediaExamples(t *testing.T, registry huma.Registry, path, status string, media privateMedia) {
	t.Helper()
	name := strings.TrimPrefix(media.Schema.Ref, schemaPrefix)
	if media.Example == nil && len(media.Examples) == 0 {
		t.Fatalf("missing %s %s example", path, status)
	}
	if media.Example != nil {
		validatePrivateExample(t, registry, name, media.Example)
	}
	for _, example := range media.Examples {
		validatePrivateExample(t, registry, name, example.Value)
	}
}

func TestCSRFRegistryPreservesMethodBoundary(t *testing.T) {
	service := &fakeAuth{}
	server := tlsServer(t, service)
	response := requestAuth(t, server, authRequestOptions{method: http.MethodPost, path: "/csrf", body: "{}", origin: server.URL, media: jsonMediaType, csrf: testToken})
	if response.StatusCode != http.StatusNotFound || service.calls != 0 {
		t.Fatal("POST csrf changed transport behavior", response.StatusCode)
	}
	// Documentation generation must not require a service or mutate routing.
	if _, err := OpenAPIJSON(); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Config{CanonicalOrigin: server.URL}, service)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/unknown", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || service.calls != 0 {
		t.Fatal("documentation changed handler boundary")
	}
}
