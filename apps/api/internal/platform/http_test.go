package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"strings"
	"testing"
)

type failingDatabase struct{}

func (failingDatabase) Ping(context.Context) error {
	return errors.New("secret database connection details")
}

func TestHealthAndPrivateContracts(t *testing.T) {
	handler := Handler(failingDatabase{}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/health/live", 200}, {"/health/ready", 503},
		{"/api/v1/content/article?language=fr", 503},
		{"/api/v1/content/article/title?language=fr", 503},
		{"/api/v1/content/article/invalid_slug?language=fr", 400},
		{"/openapi.json", 404}, {"/docs", 404}, {"/schemas", 404}, {"/api/v1/unknown", 404},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status: got %d, want %d", response.Code, test.status)
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
			if strings.Contains(response.Body.String(), "secret database") {
				t.Fatal("internal error leaked")
			}
		})
	}
}

// Проверяем сгенерированный контракт отдельно от HTTP: служебные маршруты
// выключены, а nullable-массивы и внутренние DTO не должны попасть в SDK.
func TestPublishingOpenAPI(t *testing.T) {
	_, api := newAPI(failingDatabase{}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	spec := api.OpenAPI()
	assertOfflineOpenAPI(t, spec)
	if _, err := spec.YAML(); err != nil {
		t.Fatal(err)
	}
	languages := spec.Paths["/api/v1/languages"].Get
	contents := spec.Paths["/api/v1/content/{type_key}"].Get
	detail := spec.Paths["/api/v1/content/{type_key}/{slug}"].Get
	assertPublicResponses(t, languages, "200", "400", "503")
	assertPublicResponses(t, contents, "200", "400", "404", "503")
	assertPublicResponses(t, detail, "200", "301", "400", "404", "503")
	assertRequiredLanguage(t, contents)
	assertRequiredLanguage(t, detail)
	if *parameterSchema(t, contents, "offset").Maximum != 10000 {
		t.Fatal("unbounded offset")
	}
	registry := spec.Components.Schemas
	item := registry.SchemaFromRef(detail.Responses["200"].Content["application/json"].Schema.Ref)
	assertPublicFields(t, registry, item, "canonical_url", "item_id", "language", "localized", "publication_id", "relations", "schema_no", "shared", "slug", "translations", "type_key")
	field := item.Properties["shared"].AdditionalProperties.(*huma.Schema)
	if field.Ref != "" {
		field = registry.SchemaFromRef(field.Ref)
	}
	if len(field.OneOf) != 8 || field.Discriminator.PropertyName != "kind" {
		t.Fatal("missing discriminated field union")
	}
	for _, variant := range field.OneOf {
		if len(variant.Properties["kind"].Enum) != 1 {
			t.Fatal("ambiguous field discriminator")
		}
	}

}

// Экспорт не должен создавать независимый контракт или зависеть от окружения.
func assertOfflineOpenAPI(t *testing.T, spec *huma.OpenAPI) {
	t.Helper()
	expected, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		actual, err := OpenAPIJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatal("offline export differs from registered application contract")
		}
	}
}

func assertPublicResponses(t *testing.T, operation *huma.Operation, statuses ...string) {
	t.Helper()
	for _, status := range statuses {
		if operation.Responses[status] == nil {
			t.Fatalf("%s missing response %s", operation.Path, status)
		}
	}
	if operation.Responses["422"] != nil {
		t.Fatalf("%s exposes an unsupported 422", operation.Path)
	}
	problem := operation.Responses["400"].Content["application/problem+json"]
	if problem == nil || problem.Schema == nil {
		t.Fatalf("%s missing problem schema", operation.Path)
	}
}

func parameterSchema(t *testing.T, operation *huma.Operation, name string) *huma.Schema {
	t.Helper()
	for _, parameter := range operation.Parameters {
		if parameter.Name == name {
			return parameter.Schema
		}
	}
	t.Fatalf("%s missing parameter %s", operation.Path, name)
	return nil
}

func assertRequiredLanguage(t *testing.T, operation *huma.Operation) {
	t.Helper()
	for _, parameter := range operation.Parameters {
		if parameter.Name == "language" && parameter.Required && parameter.Schema.Type == "string" {
			return
		}
	}
	t.Fatalf("%s missing required language", operation.Path)
}

func assertPublicFields(t *testing.T, registry huma.Registry, schema *huma.Schema, expected ...string) {
	t.Helper()
	if schema.Ref != "" {
		schema = registry.SchemaFromRef(schema.Ref)
	}
	fields := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		fields = append(fields, name)
	}
	slices.Sort(fields)
	if !slices.Equal(fields, expected) {
		t.Fatalf("unsafe public schema fields: %v", fields)
	}
}

func TestPublicQueriesFailClosed(t *testing.T) {
	handler := Handler(failingDatabase{}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, query := range []string{"language=fr&filter[private]=secret", "language=fr&sort=private", "language=fr&offset=10001", "language=fr&limit=101", "language=fr&language=ru", "lang=fr", "language="} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/content/article?"+query, nil))
		if response.Code != 400 {
			t.Fatalf("%s: got %d", query, response.Code)
		}
	}
	for _, path := range []string{"/api/v1/admin/content/article", "/api/v1/articles"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != 404 {
			t.Fatalf("write path %s: %d", path, response.Code)
		}
	}
}
