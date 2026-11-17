package content

import (
	"encoding/json"
	"testing"
)

func TestExactScalarCodecs(t *testing.T) {
	cases := []struct {
		kind, value string
		valid       bool
	}{{kindInteger, "9223372036854775807", true}, {kindInteger, "-9223372036854775808", true}, {kindInteger, "9223372036854775808", false}, {kindInteger, "01", false}, {kindInteger, "-0", false}, {kindDecimal, "123456789012.123456", true}, {kindDecimal, "1.2300", false}, {kindDecimal, "0.0000001", false}, {kindDecimal, "-0", false}, {kindDatetime, "2026-11-17T12:00:00.123456Z", true}, {kindDatetime, "2026-11-17T12:00:00.1234567Z", false}}
	for _, test := range cases {
		t.Run(test.kind+test.value, func(t *testing.T) {
			raw, _ := json.Marshal(test.value)
			got, err := validateValue(Definition{Kind: test.kind}, FieldValue{test.kind, raw})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
			if err == nil && string(got) != string(raw) {
				t.Fatal("codec changed exact scalar")
			}
		})
	}
}
func TestRegistryValidation(t *testing.T) {
	input := initialType()
	if err := ValidateType(input); err != nil {
		t.Fatal(err)
	}
	input.Fields[0].Config = []byte(`{"regex":".*"}`)
	if ValidateType(input) == nil {
		t.Fatal("executable/unregistered validator accepted")
	}
	input = initialType()
	input.Fields[1].Visibility = visibilityPrivate
	if ValidateType(input) == nil {
		t.Fatal("private route field accepted")
	}
}
func TestPatchValidation(t *testing.T) {
	schema := TypeSchema{Fields: []Definition{{Key: "n", Kind: kindInteger, Scope: scopeShared, Visibility: visibilityPublic}}}
	current := ScalarValues{"n": json.RawMessage(`"5"`)}
	values, changed, err := patchValues(schema, scopeShared, current, ValuePatch{})
	if err != nil || changed || string(values["n"]) != `"5"` {
		t.Fatal("omitted key changed")
	}
	for _, patch := range []ValuePatch{{Values: map[string]FieldValue{"n": {kindInteger, json.RawMessage(` null `)}}}, {Values: map[string]FieldValue{"unknown": {kindInteger, json.RawMessage(`"1"`)}}}, {Values: map[string]FieldValue{"n": {kindInteger, json.RawMessage(`1`)}}}} {
		if _, _, err := patchValues(schema, scopeShared, current, patch); err == nil {
			t.Fatal("unsafe scalar patch accepted")
		}
	}
}
func TestSchemaChangesAreAdditiveOptional(t *testing.T) {
	input := initialType()
	for i := range input.Fields {
		input.Fields[i].ID = input.Fields[i].Key
	}
	schema := TypeSchema{Fields: input.Fields}
	fields := append([]Definition{}, input.Fields...)
	fields[0].DisplayLabels = []byte(`{"en":{"label":"Changed"}}`)
	_, semantic, err := revisionFields(schema, fields)
	if err != nil || semantic {
		t.Fatal("display-only change became semantic")
	}
	fields = append(fields, Definition{Key: "extra", Kind: kindBoolean, Scope: scopeShared, Visibility: visibilityPublic})
	if _, semantic, err := revisionFields(schema, fields); err != nil || !semantic {
		t.Fatal("optional addition rejected")
	}
	fields[0].Kind = kindText
	if _, _, err := revisionFields(schema, fields); err == nil {
		t.Fatal("incompatible field edit accepted")
	}
}

func TestRegisteredRendererDependencies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*TypeInput)
	}{
		{"missing title", func(input *TypeInput) { input.Fields = input.Fields[1:] }},
		{"wrong kind", func(input *TypeInput) { input.Fields[0].Kind = kindText }},
		{"private title", func(input *TypeInput) { input.Fields[0].Visibility = visibilityPrivate }},
		{"optional title", func(input *TypeInput) { input.Fields[0].Required = false }},
		{"shared title", func(input *TypeInput) { input.Fields[0].Scope = scopeShared }},
		{"empty title allowed", func(input *TypeInput) { input.Fields[0].Config = []byte(`{}`) }},
	}
	for _, namespace := range []string{articlesNamespace, eventsNamespace} {
		for _, test := range cases {
			t.Run(namespace+"/"+test.name, func(t *testing.T) {
				input := initialType()
				input.RouteNamespace = namespace
				input.Key = rendererTypeKey(namespace)
				if err := ValidateType(input); err != nil {
					t.Fatalf("valid registered profile rejected: %v", err)
				}
				test.mutate(&input)
				if ValidateType(input) == nil {
					t.Fatal("incompatible renderer profile accepted")
				}
			})
		}
	}
}

func TestRegisteredRendererTypeMapping(t *testing.T) {
	for _, test := range []struct{ key, namespace string }{{"conference", eventsNamespace}, {initialArticleKey, eventsNamespace}, {testEventKey, articlesNamespace}} {
		input := initialType()
		input.Key = test.key
		input.RouteNamespace = test.namespace
		if ValidateType(input) == nil {
			t.Fatal("unrenderable type/namespace mapping accepted")
		}
	}
}
