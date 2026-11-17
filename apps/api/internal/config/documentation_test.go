package config

import "testing"

func TestDocumentationExplicitOptIn(t *testing.T) {
	tests := []struct {
		input          string
		enabled, valid bool
	}{
		{input: "", valid: true},
		{input: "false", valid: true},
		{input: "true", enabled: true, valid: true},
		{input: "1"},
		{input: "TRUE"},
		{input: "production"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			t.Setenv("API_DOCS_ENABLED", test.input)
			enabled, err := DocumentationEnabled()
			if (err == nil) != test.valid || enabled != test.enabled {
				t.Fatalf("value=%q enabled=%t error=%v", test.input, enabled, err)
			}
		})
	}
}
