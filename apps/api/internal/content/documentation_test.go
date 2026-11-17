package content_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"stepolonia/apps/api/internal/apiexamples"

	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/platform"
)

const (
	sharedScope      = "shared"
	localizedScope   = "localized"
	publicVisibility = "public"
)

func documentationSchemas() map[string]content.TypeSchema {
	article := content.DocumentationInitialType()
	// Scalar validation does not resolve relation identities. Real fixture tests
	// bind the Event descriptor to the installation's Article type UUID.
	event := apiexamples.EventSchema("")
	return map[string]content.TypeSchema{
		article.Key: {Key: article.Key, SchemaNo: 1, Fields: article.Fields},
		event.Key:   event,
	}
}

func TestDocumentedScalarsUseRegistryCodecs(t *testing.T) {
	raw, err := platform.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]struct {
			Get struct {
				Responses map[string]struct {
					Content map[string]struct {
						Examples map[string]struct{ Value json.RawMessage }
					} `json:"content"`
				} `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	examples := spec.Paths["/api/v1/content/{type_key}/{slug}"].Get.Responses["200"].Content["application/json"].Examples
	if len(examples) < 2 {
		t.Fatal("Article and Event examples are required")
	}
	schemas := documentationSchemas()
	for name, example := range examples {
		t.Run(name, func(t *testing.T) {
			var item content.PublicItem
			if err := json.Unmarshal(example.Value, &item); err != nil {
				t.Fatal(err)
			}
			schema, exists := schemas[item.TypeKey]
			if !exists || item.SchemaNo != strconv.FormatInt(schema.SchemaNo, 10) {
				t.Fatal("example has no matching pinned demo schema")
			}
			assertDocumentedValues(t, schema, sharedScope, item.Shared)
			assertDocumentedValues(t, schema, localizedScope, item.Localized)
		})
	}
}

func assertDocumentedValues(t *testing.T, schema content.TypeSchema, scope string, values map[string]content.PublicFieldValue) {
	t.Helper()
	fields := map[string]content.Definition{}
	for _, field := range schema.Fields {
		fields[field.Key] = field
	}
	scalars := content.ScalarValues{}
	for key, tagged := range values {
		field, exists := fields[key]
		if !exists || field.Scope != scope || field.Visibility != publicVisibility {
			t.Fatalf("example exposes unsupported public field %s", key)
		}
		value, err := content.DocumentationValidateValue(field, content.FieldValue(tagged))
		if err != nil {
			t.Fatalf("%s violates its pinned field definition: %v", key, err)
		}
		scalars[key] = value
	}
	if err := content.DocumentationValidateValues(schema, scope, scalars, true); err != nil {
		t.Fatalf("example violates required fields or collection limits: %v", err)
	}
}
