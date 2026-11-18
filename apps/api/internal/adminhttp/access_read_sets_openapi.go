package adminhttp

import (
	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

const accessEditorOptionsField = "options"

func addAccessReadSetSchemas(schemas map[string]*huma.Schema) {
	roleOption := roleMembershipOption{ID: exampleUUID, Name: "Editors", State: "disabled"}
	schemas["RoleMembershipOption"] = responseDTOSchema("Named role and lifecycle state for account membership selection.", roleOption)
	roleSet := roleMembershipSetResponse{UserID: exampleUUID, Version: 2, RoleIDs: []string{exampleUUID}, Options: []roleMembershipOption{roleOption}, MaxRoles: identity.MaxUserRoles}
	schema := responseDTOSchema("Complete account membership set and bounded named choices.", roleSet)
	schema.Properties[roleIDsField] = accessReadSetIDs(identity.MaxUserRoles)
	schema.Properties[accessEditorOptionsField] = accessReadSetOptions("RoleMembershipOption", identity.MaxRolePageLimit)
	schema.AdditionalProperties = false
	schemas[roleMembershipSetSchema] = schema

	assigneeOption := contentAssigneeOption{ID: exampleUUID, Login: "editor", State: "active"}
	schemas["ContentAssigneeOption"] = responseDTOSchema("Named eligible account or an existing inactive assignee.", assigneeOption)
	assignmentSet := contentAssignmentSetResponse{
		contentAssigneesResponse: contentAssigneesResponse{ItemID: exampleUUID, Version: 2, AssigneeIDs: []string{exampleUUID}},
		Options:                  []contentAssigneeOption{assigneeOption},
		MaxAssignees:             identity.MaxContentAssignees,
	}
	schema = contentAssigneesResultSchema()
	schema.Description = "Complete item assignment set and bounded named choices."
	schema.Properties[accessEditorOptionsField] = accessReadSetOptions("ContentAssigneeOption", identity.MaxContentAssignees)
	schema.Properties["max_assignees"] = &huma.Schema{Type: huma.TypeInteger, Minimum: floatPointer(1)}
	schema.Required = append(schema.Required, accessEditorOptionsField, "max_assignees")
	schema.Examples = []any{assignmentSet}
	schemas[contentAssignmentSetSchema] = schema
}

func accessReadSetOptions(ref string, limit int) *huma.Schema {
	return &huma.Schema{Type: huma.TypeArray, Items: schemaRef(ref), MaxItems: intPointer(limit)}
}

func accessReadSetIDs(limit int) *huma.Schema {
	return &huma.Schema{Type: huma.TypeArray, Items: uuidSchema(exampleUUID), MaxItems: intPointer(limit), UniqueItems: true}
}

func accessReadSetErrorCodes() map[int][]string {
	return map[int][]string{
		400: {validationCode}, 401: {authenticationCode}, 403: {originCode, permissionCode},
		404: {notFoundCode}, 405: {methodNotAllowedCode}, 503: {securityUnavailableCode},
	}
}
