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
	articles := spec.Paths["/api/v1/articles"].Get
	detail := spec.Paths["/api/v1/articles/{slug}"].Get
	assertPublicResponses(t, languages, "200", "400", "503")
	assertPublicResponses(t, articles, "200", "400", "404", "503")
	assertPublicResponses(t, detail, "200", "301", "400", "404", "503")
	assertPublicArray(t, languages.Responses["200"].Content["application/json"].Schema)
	assertPublicArray(t, articles.Responses["200"].Content["application/json"].Schema)
	assertRequiredLanguage(t, articles)
	assertRequiredLanguage(t, detail)
	limit := parameterSchema(t, articles, "limit")
	if limit.Default != 20 || *limit.Minimum != 1 || *limit.Maximum != 100 {
		t.Fatalf("invalid pagination schema: %+v", limit)
	}
	redirect := detail.Responses["301"]
	if redirect.Headers["Location"] == nil {
		t.Fatal("redirect Location is undocumented")
	}
	if redirect.Content["application/json"].Schema.Type != huma.TypeNull {
		t.Fatal("redirect JSON null body is undocumented")
	}
	registry := spec.Components.Schemas
	assertPublicFields(t, registry, languages.Responses["200"].Content["application/json"].Schema.Items, "code", "direction", "name", "native_name")
	articleFields := []string{"article_id", "body_markdown", "language_code", "slug", "title"}
	assertPublicFields(t, registry, articles.Responses["200"].Content["application/json"].Schema.Items, articleFields...)
	const translationsField = "published_translations"
	detailFields := append(slices.Clone(articleFields), translationsField)
	slices.Sort(detailFields)
	detailSchema := detail.Responses["200"].Content["application/json"].Schema
	assertPublicFields(t, registry, detailSchema, detailFields...)
	detailSchema = registry.SchemaFromRef(detailSchema.Ref)
	if !slices.Contains(detailSchema.Required, translationsField) {
		t.Fatal("published translations are optional in the detail contract")
	}
	translations := detailSchema.Properties[translationsField]
	assertPublicArray(t, translations)
	assertPublicFields(t, registry, translations.Items, "language_code", "slug")
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

func assertPublicArray(t *testing.T, schema *huma.Schema) {
	t.Helper()
	if schema.Type != "array" || schema.Nullable {
		t.Fatalf("expected a non-null array: %+v", schema)
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
		if parameter.Name == "lang" && parameter.Required && parameter.Schema.Type == "string" {
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
