package cmshttp

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestPrivateRolesContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	for _, path := range []string{rolesPath, rolePath} {
		if len(doc.Paths[path]) != 2 {
			t.Fatal("role operation registry drifted", path)
		}
		assertRoleReadContract(t, doc.Paths[path]["get"])
	}
	if doc.Paths[rolesPath]["post"].OperationID != "rolesCreate" || doc.Paths[rolePath]["patch"].OperationID != "rolesEdit" {
		t.Fatal("role writes changed verbs or operation identities")
	}
	patch := doc.Paths[rolePath]["patch"]
	if _, exists := patch.Responses["200"].Headers["Set-Cookie"]; exists {
		t.Fatal("role edit promises cookie mutation")
	}
	if len(doc.Components.Schemas[roleSchema].Properties) != 5 || *doc.Components.Schemas[roleSchema].Properties["grants"].MaxItems != 64 {
		t.Fatal("safe role projection or policy cap drifted")
	}
}

func assertRoleReadContract(t *testing.T, op privateOperation) {
	t.Helper()
	if len(op.Tags) != 1 || op.Tags[0] != "roles" || len(op.Security) != 1 || op.Security[0]["__Host-sp_session"] == nil {
		t.Fatal("role read leaked into another group or authentication kind")
	}
	if _, exists := op.Responses["200"].Headers["Set-Cookie"]; exists {
		t.Fatal("role read promises cookie mutation")
	}
	if _, exists := op.Responses["429"]; exists {
		t.Fatal("role read promises denial budget consumption")
	}
	for _, parameter := range op.Parameters {
		if parameter.Name == testCSRFHeader {
			t.Fatal("role read requires CSRF")
		}
	}
}

func TestPrivateRolesGrantSchemaRejectsWrongShapes(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	registry := privateRegistry(doc)
	schema := doc.Components.Schemas[roleGrantSchema]
	for _, raw := range []string{
		`{"operation":"users.read","scope":"installation","can_delegate":false,"type_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"operation":"content.items.read","scope":"installation","can_delegate":false}`,
		`{"operation":"content.items.create","scope":"content","can_delegate":false,"type_id":"11111111-1111-4111-8111-111111111111","object_scope":"any"}`,
		`{"operation":"content.permissions.delegate","scope":"installation","can_delegate":false,"target_scope":"installation","object_ceiling":"any","may_delegate_content":false,"delegate_operations":["roles.edit"]}`,
	} {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		privateSchemaRejects(t, registry, schema, value)
	}
	for _, variant := range schema.OneOf {
		if variant.AdditionalProperties != false || !slices.Contains(variant.Required, "can_delegate") {
			t.Fatal("grant shape does not require explicit delegation policy")
		}
	}
}
