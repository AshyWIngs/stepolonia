package adminhttp

import (
	"encoding/json"
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
	for _, raw := range []string{`{}`, `{"expected_version":7}`, `{"expected_version":"01"}`,
		`{"expected_version":"1","expected_version":"2"}`, `{"Expected_version":"1"}`,
		`{"expected_version":"1","extra":false}`, `{"expected_version":"1"}{}`,
	} {
		var input settingsInput
		if decodeSettings([]byte(raw), settingsRequestSchema(publishSettingsOperation), &input) {
			t.Fatalf("accepted invalid publication input %s", raw)
		}
	}
}
