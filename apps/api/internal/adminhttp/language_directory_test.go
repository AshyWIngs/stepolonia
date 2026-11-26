package adminhttp

import (
	"strings"
	"testing"
)

func TestLanguageDirectoryWriteSchemasFollowDTO(t *testing.T) {
	const definition = `"language":{"code":"pl","name":"Polish","native_name":"Polski","direction":"ltr"}`
	if !decodeClosedCommand([]byte("{"+definition+"}"), languageSaveSchema, &languageSaveInput{}) {
		t.Fatal("valid definition rejected")
	}
	bad := []string{
		`{"language":null}`,
		"{" + definition + `,"expected_version":"old"}`,
		"{" + strings.Replace(definition, `"ltr"`, `"sideways"`, 1) + "}",
		"{" + strings.Replace(definition, `"Polish"`, `"Polish","Name":"unexpected"`, 1) + "}",
		"{" + strings.Replace(definition, `"Polish"`, `"`+strings.Repeat("a", 201)+`"`, 1) + "}",
	}
	for _, body := range bad {
		if decodeClosedCommand([]byte(body), languageSaveSchema, &languageSaveInput{}) {
			t.Errorf("invalid definition accepted: %s", body)
		}
	}
	body := `{"code":"pl","enabled":false,"expected_version":"` + strings.Repeat("a", 64) + `"}`
	var availability languageAvailabilityInput
	if !decodeClosedCommand([]byte(body), languageAvailabilityRequestSchema, &availability) || availability.Enabled {
		t.Fatal("explicit false rejected")
	}
}
