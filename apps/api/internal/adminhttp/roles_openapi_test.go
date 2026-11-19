package adminhttp

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

const expectedRolesTag = "roles"

func TestPrivateRolesContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	for path, count := range map[string]int{rolesPath: 2, rolePath: 3} {
		if len(doc.Paths[path]) != count {
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
	role := doc.Components.Schemas[roleSchema]
	if len(role.Properties) != 7 || *role.Properties["grants"].MaxItems != 64 {
		t.Fatal("safe role projection or policy cap drifted")
	}
	for _, name := range []string{roleSchema, "RoleMembershipOption"} {
		schema := doc.Components.Schemas[name]
		labels := schema.Properties["display_names"]
		if labels == nil || labels.Type != huma.TypeObject || slices.Contains(schema.Required, "display_names") {
			t.Fatal("role display names must be optional localized maps", name)
		}
	}
	methods := doc.Paths[rolePath+"/display-name"]
	if len(methods) != 1 || methods["post"].OperationID != roleDisplayNameOperation {
		t.Fatal("role display name command drifted")
	}
	assertRoleCollectionKeyContract(t, doc)
}

func assertRoleCollectionKeyContract(t *testing.T, doc privateDocument) {
	t.Helper()
	const field = "collection_keys"
	role := doc.Components.Schemas["Role"]
	keys := role.Properties[field]
	if keys == nil {
		t.Fatal("role response omits collection keys")
	}
	values, ok := keys.AdditionalProperties.(*huma.Schema)
	if keys.Type != huma.TypeObject || !ok || values.Type != huma.TypeString || slices.Contains(role.Required, field) {
		t.Fatal("collection keys must be an optional string map on read")
	}
	for _, operation := range []string{createRoleOperation, editRoleOperation} {
		if _, ok := doc.Components.Schemas[operation+"Request"].Properties[field]; ok {
			t.Fatal("collection keys exposed in role write schema", operation)
		}
	}
}

func assertRoleReadContract(t *testing.T, op privateOperation) {
	t.Helper()
	if len(op.Tags) != 1 || op.Tags[0] != expectedRolesTag || len(op.Security) != 1 || op.Security[0]["__Host-sp_session"] == nil {
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

func TestPrivateRoleStateContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	for _, tc := range roleStateCases {
		methods := doc.Paths["/api/v1/admin/roles/{id}/"+tc.action]
		op := methods["post"]
		if len(methods) != 1 || op.OperationID != tc.operation || !slices.Equal(op.Tags, []string{expectedRolesTag}) || len(op.Security) != 1 || op.Security[0]["__Host-sp_session"] == nil {
			t.Fatal("role state operation, tag or auth scheme drifted", tc.action)
		}
		assertRoleVersionSchema(t, doc, op)
		success := op.Responses["200"].Content["application/json"]
		if success.Schema.Ref != "#/components/schemas/Role" || success.Example.(map[string]any)["state"] != tc.state {
			t.Fatal("wrong resulting role in documentation", tc.action)
		}
		for _, status := range []string{"400", "401", "403", "404", "405", "409", "413", "429", "503"} {
			if _, ok := op.Responses[status]; !ok {
				t.Fatal("undocumented role state response", tc.action, status)
			}
		}
	}
}

const expectedRoleVersionProperty = "expected_version"

func assertRoleVersionSchema(t *testing.T, doc privateDocument, op privateOperation) {
	t.Helper()
	input := doc.Components.Schemas[op.OperationID+"Request"]
	if input == nil || len(input.Properties) != 1 || !slices.Equal(input.Required, []string{expectedRoleVersionProperty}) || input.AdditionalProperties != false {
		t.Fatal("role state command must accept only expected_version", op.OperationID)
	}
	version := input.Properties[expectedRoleVersionProperty]
	if version.Type != "integer" || version.Minimum == nil || *version.Minimum != 1 {
		t.Fatal("role version must be positive", op.OperationID)
	}
}

func TestPrivateRoleCopyDeleteContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	copyOp := doc.Paths["/api/v1/admin/roles/{id}/copy"]["post"]
	deleteOp := doc.Paths["/api/v1/admin/roles/{id}"]["delete"]
	if copyOp.OperationID != "rolesCopy" || deleteOp.OperationID != "rolesDelete" {
		t.Fatal("role copy/delete operation identities drifted")
	}
	assertRoleCommandSecurity(t, copyOp)
	assertRoleCommandSecurity(t, deleteOp)
	assertRoleVersionSchema(t, doc, deleteOp)
	if _, ok := deleteOp.Responses["409"].Content["application/problem+json"].Examples["role_in_use"]; !ok {
		t.Fatal("missing role_in_use delete conflict")
	}
	input := doc.Components.Schemas[copyOp.OperationID+"Request"]
	if input == nil || input.AdditionalProperties != false || len(input.Properties) != 2 || !slices.Equal(input.Required, []string{"name", expectedRoleVersionProperty}) {
		t.Fatal("copy must accept only a new name and source version")
	}
	if copyOp.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/Role" {
		t.Fatal("copy must return a role")
	}
	if success, ok := deleteOp.Responses["204"]; !ok || len(success.Content) != 0 {
		t.Fatal("delete must return an empty response")
	}
}

func assertRoleCommandSecurity(t *testing.T, op privateOperation) {
	t.Helper()
	if !slices.Equal(op.Tags, []string{expectedRolesTag}) || len(op.Security) != 1 || op.Security[0]["__Host-sp_session"] == nil {
		t.Fatal("role command group or auth scheme drifted", op.OperationID)
	}
	for _, status := range []string{"403", "404", "409", "429", "503"} {
		if _, ok := op.Responses[status]; !ok {
			t.Fatal("missing role command response", op.OperationID, status)
		}
	}
}
