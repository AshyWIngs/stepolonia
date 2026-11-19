package adminhttp

import (
	"encoding/json"
	"strings"
	"testing"
)

func checkSettingsRequestExample(t *testing.T, op privateOperation, rt route) {
	t.Helper()
	body, err := json.Marshal(op.RequestBody.Content[jsonMediaType].Example)
	var input settingsInput
	if err != nil || !decodeSettings(body, settingsRequestSchema(rt.operationID), &input) {
		t.Fatalf("settings example rejected: %s: %v", rt.operationID, err)
	}
}

func TestSettingsMutationsRejectInvalidVersionsAndProperties(t *testing.T) {
	schema := settingsRequestSchema(applySettingsOperation)
	body, err := json.Marshal(schema.Examples[0])
	if err != nil {
		t.Fatal(err)
	}
	valid := string(body)
	var good settingsInput
	if !decodeSettings(body, schema, &good) {
		t.Fatal("reference settings request was rejected")
	}
	const version = `"expected_version":"1"`
	for _, property := range []string{`"expected_version":987`, `"expected_version":"01"`,
		`"expected_version":"1","expected_version":"2"`, `"Expected_version":"1"`,
		`"expected_version":"1","extra":false`} {
		raw := strings.Replace(valid, version, property, 1)
		var input settingsInput
		if decodeSettings([]byte(raw), schema, &input) {
			t.Fatalf("accepted invalid settings input %s", property)
		}
	}
	var trailing settingsInput
	if decodeSettings([]byte(valid+`{}`), schema, &trailing) {
		t.Fatal("accepted a trailing object")
	}
}
