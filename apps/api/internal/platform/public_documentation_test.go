package platform

import (
	"encoding/json"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestPublicDocumentationExamples(t *testing.T) {
	_, api := newAPI(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	spec := api.OpenAPI()
	for _, path := range spec.Paths {
		operation := path.Get
		t.Run(operation.OperationID, func(t *testing.T) {
			assertPublicOperationDocumentation(t, spec.Components.Schemas, operation)
		})
	}
	for name := range spec.Components.Schemas.Map() {
		t.Run(name, func(t *testing.T) {
			assertPublicModelDocumentation(t, spec.Components.Schemas, name)
		})
	}
}

func assertPublicOperationDocumentation(t *testing.T, registry huma.Registry, operation *huma.Operation) {
	t.Helper()
	if len(strings.Fields(operation.Description)) < 20 {
		t.Fatalf("operation requires behavioral documentation: %q", operation.Description)
	}
	for _, parameter := range operation.Parameters {
		if parameter.Description == "" || len(parameter.Schema.Examples) == 0 {
			t.Errorf("parameter %s requires description and example", parameter.Name)
		}
	}
	for status, response := range operation.Responses {
		if len(strings.Fields(response.Description)) < 5 {
			t.Errorf("response %s requires a semantic description", status)
		}
		for mediaType, media := range response.Content {
			t.Run(status+"/"+mediaType, func(t *testing.T) {
				if status == "301" && media.Schema.Type != huma.TypeNull {
					t.Error("historical-slug body must remain JSON null")
				}
				assertPublicMediaExamples(t, registry, media)
			})
		}
	}
}

func assertPublicMediaExamples(t *testing.T, registry huma.Registry, media *huma.MediaType) {
	t.Helper()
	values := []any{}
	if media.Example != nil {
		values = append(values, media.Example)
	}
	for _, example := range media.Examples {
		values = append(values, example.Value)
	}
	if len(values) == 0 {
		t.Fatal("response lacks examples")
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err = json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		result := &huma.ValidateResult{}
		huma.Validate(registry, media.Schema, &huma.PathBuffer{}, huma.ModeReadFromServer, decoded, result)
		if len(result.Errors) > 0 {
			t.Errorf("example violates schema: %v", result.Errors)
		}
	}
}

func assertPublicModelDocumentation(t *testing.T, registry huma.Registry, name string) {
	t.Helper()
	schema := registry.SchemaFromRef("#/components/schemas/" + name)
	if schema == nil {
		t.Fatalf("missing model %s", name)
	}
	if len(strings.Fields(schema.Description)) < 8 {
		t.Errorf("model %s lacks behavioral documentation", name)
	}
	for property, definition := range schema.Properties {
		if len(strings.Fields(definition.Description)) < 4 {
			t.Errorf("%s.%s lacks property documentation", name, property)
		}
	}
}

func TestPublicDocumentationDomainGroups(t *testing.T) {
	_, api := newAPI(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	expected := map[string][]string{
		"health":            {"liveness", "readiness"},
		"site-publication":  {"getSite"},
		"content-languages": {"listLanguages"},
		"published-content": {"listContent", "getContent"},
	}
	declared := map[string]bool{}
	for _, tag := range api.OpenAPI().Tags {
		if _, ok := expected[tag.Name]; !ok || tag.Description == "" || declared[tag.Name] || !regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`).MatchString(tag.Name) {
			t.Fatalf("invalid public domain tag: %+v", tag)
		}
		declared[tag.Name] = true
	}
	if len(declared) != len(expected) {
		t.Fatal("missing public domain tags")
	}
	for _, path := range api.OpenAPI().Paths {
		op := path.Get
		if len(op.Tags) != 1 || !declared[op.Tags[0]] || !slices.Contains(expected[op.Tags[0]], op.OperationID) {
			t.Fatalf("incorrect domain for %s: %v", op.OperationID, op.Tags)
		}
	}
}
