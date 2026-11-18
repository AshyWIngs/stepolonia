package adminhttp

import (
	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

func addRoleGrantOptionSchemas(schemas map[string]*huma.Schema) {
	const (
		optionSchemaName = "RoleGrantOption"
		fieldSchemaName  = "RoleGrantOptionField"
	)
	field := roleGrantOptionField{ID: exampleUUID, Key: "title"}
	schemas[fieldSchemaName] = responseDTOSchema("An eligible field's stable identifier and machine key.", field)
	option := roleGrantOptionDTO{
		roleGrantDTO: roleGrantDTO{Operation: string(identity.OpContentRead), Scope: roleContentScope, TypeID: exampleUUID, ObjectScope: roleAssignedObjects},
		TypeKey:      registryExample().TypeKey,
		Fields:       []roleGrantOptionField{field},
	}
	schema := responseDTOSchema("One covering grant shape. A write can reduce eligible fields, delegated operations and delegation flags, subject to fresh server authorization.", option.roleGrantDTO)
	delete(schema.Properties, "field_ids")
	schema.Properties["type_key"] = stringSchema("Stable machine key of the eligible collection.", option.TypeKey)
	schema.Properties["fields"] = &huma.Schema{Type: huma.TypeArray, Items: schemaRef(fieldSchemaName)}
	schema.Properties["type_id"].Format = "uuid"
	schema.AdditionalProperties = false
	schema.Examples = []any{option}
	schemas[optionSchemaName] = schema
	schemas[roleGrantOptionsSchema] = closedObjectSchema(
		"Bounded collection of eligible grant options with no role recipients or content data.",
		map[string]*huma.Schema{accessEditorOptionsField: accessReadSetOptions(optionSchemaName, identity.MaxRoleGrantOptions)},
		[]string{accessEditorOptionsField}, roleGrantOptionsResponse{Options: []roleGrantOptionDTO{option}},
	)
}
