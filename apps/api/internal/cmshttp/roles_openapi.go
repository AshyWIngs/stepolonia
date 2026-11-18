package cmshttp

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/cms"
)

const roleAuthorityOperation = "content.permissions.delegate"

const (
	roleInstallationScope = "installation"
	roleContentScope      = "content"
)

const (
	roleAssignedObjects = "assigned"
	roleAnyObjects      = "any"
)

const rolesTag = "roles"

const (
	roleSchema      = "Role"
	roleListSchema  = "RoleList"
	roleGrantSchema = "RoleGrant"
)

func roleGrantExample() roleGrantDTO {
	return roleGrantDTO{Operation: "users.read", Scope: roleInstallationScope, CanDelegate: false}
}

func roleRequestSchema(rt route) (*huma.Schema, map[string]any) {
	example := map[string]any{"name": "docs-reviewer", "grants": []roleGrantDTO{roleGrantExample()}}
	schema := responseDTOSchema("Complete explicit role policy. Name and grants are required; no actor, state, memberships, recipient list or implicit grant expansion is accepted.", roleWriteInput{})
	schema.Examples = []any{example}
	replaceRoleGrantSchema(schema)
	if rt.method == http.MethodPatch {
		schema.Required = append(schema.Required, expectedVersionField)
		example[expectedVersionField] = 1
	} else {
		delete(schema.Properties, expectedVersionField)
	}
	return schema, example
}

func replaceRoleGrantSchema(schema *huma.Schema) {
	schema.Properties["grants"] = &huma.Schema{Type: huma.TypeArray, Items: schemaRef(roleGrantSchema), MaxItems: intPointer(cms.MaxRoleGrants), Description: "Complete explicit grant set; duplicated operation/type/object scopes and field IDs are rejected. Empty grants removes all access."}
}

func addRoleSchemas(schemas map[string]*huma.Schema) {
	schemas[roleGrantSchema] = roleGrantContract()
	example := roleResponse{ID: syntheticUserID, Name: "docs-reviewer", State: "disabled", Version: 1, Grants: []roleGrantDTO{roleGrantExample()}}
	schema := responseDTOSchema("Safe role policy metadata including explicit grants and latent disabled/archived grants. No credentials, memberships, recipients, content values or field labels are exposed.", example)
	replaceRoleGrantSchema(schema)
	schemas[roleSchema] = schema
	schemas[roleListSchema] = privatePageSchema("Bounded safe policy page in stable stored-name and UUID order. Installation-wide roles.read is independent of the edit envelope; next_offset is null at the end.", roleSchema, cms.MaxRolePageLimit, cms.MaxRolePageOffset, roleListResponse{Items: []roleResponse{example}, NextOffset: nil})
}

func roleGrantContract() *huma.Schema {
	mayDelegate := false
	installation := []string{"content_types.manage", "content_types.initialize_access", "users.create", "users.read", "users.activate", "users.disable", "roles.create", "roles.read", "roles.edit", "roles.assign", "roles.disable", "roles.enable", "roles.archive", "roles.restore", "roles.delete", "sessions.revoke_any", "audit.read", "settings.manage"}
	content := []string{string(cms.OpContentRead), "content.items.update", "content.items.ready", "content.items.publish", "content.items.unpublish", "content.items.assign"}
	allContent := append([]string{"content.items.create"}, content...)
	security := roleGrantVariant(installation, roleInstallationScope)
	create := roleGrantVariant([]string{"content.items.create"}, roleContentScope)
	addContentGrantProperties(create, []string{"type"})
	data := roleGrantVariant(content, roleContentScope)
	addContentGrantProperties(data, []string{roleAssignedObjects, roleAnyObjects})
	installationAuthority := roleAuthoritySchema(roleInstallationScope, allContent)
	typeAuthority := roleAuthoritySchema("type", allContent)
	typeAuthority.Properties["type_id"] = uuidSchema(syntheticUserID)
	typeAuthority.Properties["field_ids"] = roleFieldsSchema()
	typeAuthority.Required = append(typeAuthority.Required, "type_id")
	return &huma.Schema{Description: "One explicit registered grant. Choose exactly one closed shape: installation security, content create, content item access, installation delegation authority or type delegation authority. Irrelevant, null and unknown properties are rejected even when empty or false. Authority grants permission to delegate policies, without content data access.", OneOf: []*huma.Schema{security, create, data, installationAuthority, typeAuthority}, Examples: []any{roleGrantExample(), roleGrantDTO{Operation: string(cms.OpContentRead), Scope: roleContentScope, TypeID: syntheticUserID, ObjectScope: roleAssignedObjects, FieldIDs: []string{syntheticUserID}}, roleGrantDTO{Operation: roleAuthorityOperation, Scope: roleInstallationScope, TargetScope: roleInstallationScope, ObjectCeiling: roleAnyObjects, MayDelegateContent: &mayDelegate, DelegateOperations: []string{string(cms.OpContentRead)}}}}
}

func roleGrantVariant(operations []string, scope string) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: false, Required: []string{"operation", "scope", "can_delegate"}, Properties: map[string]*huma.Schema{
		"operation":    {Type: huma.TypeString, Enum: stringValues(operations), Description: "Exact registered operation; new or unknown operations never expand implicitly."},
		"scope":        {Type: huma.TypeString, Const: scope, Description: "Registered grant category."},
		"can_delegate": {Type: huma.TypeBoolean, Description: "Whether the grant may itself be delegated; bounded by the actor's current envelope."},
	}}
}

func addContentGrantProperties(schema *huma.Schema, scopes []string) {
	schema.Properties["type_id"] = uuidSchema(syntheticUserID)
	schema.Properties["object_scope"] = &huma.Schema{Type: huma.TypeString, Enum: stringValues(scopes), Description: "Create has type scope; other content actions require assigned or any."}
	schema.Properties["field_ids"] = roleFieldsSchema()
	schema.Required = append(schema.Required, "type_id", "object_scope")
}

func roleFieldsSchema() *huma.Schema {
	return &huma.Schema{Type: huma.TypeArray, Items: uuidSchema(syntheticUserID), UniqueItems: true, Description: "Exact existing field UUIDs belonging to type_id. Omitted or empty means no field access; no wildcard or future field expansion."}
}

func roleAuthoritySchema(target string, operations []string) *huma.Schema {
	schema := roleGrantVariant([]string{roleAuthorityOperation}, roleInstallationScope)
	schema.Properties["target_scope"] = &huma.Schema{Type: huma.TypeString, Const: target}
	schema.Properties["object_ceiling"] = &huma.Schema{Type: huma.TypeString, Enum: []any{roleAssignedObjects, roleAnyObjects}}
	schema.Properties["may_delegate_content"] = &huma.Schema{Type: huma.TypeBoolean, Description: "Whether the authority can issue delegable content grants; independent of can_delegate."}
	schema.Properties["delegate_operations"] = &huma.Schema{Type: huma.TypeArray, Items: &huma.Schema{Type: huma.TypeString, Enum: stringValues(operations)}, MinItems: intPointer(1), MaxItems: intPointer(7), UniqueItems: true}
	schema.Required = append(schema.Required, "target_scope", "object_ceiling", "may_delegate_content", "delegate_operations")
	return schema
}

func rolePageParameters() []*huma.Param {
	return privatePageParameters(cms.DefaultRolePageLimit, cms.MaxRolePageLimit, cms.MaxRolePageOffset)
}

func roleErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{400: {validationCode}, 401: {authenticationCode}, 403: {originCode, permissionCode}, 405: {methodNotAllowedCode}, 503: {securityUnavailableCode}}
	if rt.operationID == getRoleOperation || rt.operationID == editRoleOperation {
		codes[404] = []string{notFoundCode}
	}
	if rt.method != http.MethodGet {
		codes[403] = append(codes[403], csrfCode, stepUpRequiredCode)
		codes[409] = []string{versionConflictCode, invalidTransitionCode, lastAdminRequiredCode}
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	return codes
}

func roleSuccessDescription(rt route) string {
	switch rt.operationID {
	case listRolesOperation:
		return "Bounded role policy page returned after current roles.read authorization; next_offset is null at the end."
	case getRoleOperation:
		return "Safe explicit role policy returned after current roles.read authorization."
	case createRoleOperation:
		return "Disabled role and explicit grants committed with audit; no memberships created."
	default:
		return "Role name and complete explicit grants replaced with a version increment and atomic audit; state and memberships preserved."
	}
}
