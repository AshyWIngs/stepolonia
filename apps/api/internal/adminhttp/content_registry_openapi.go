package adminhttp

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const (
	registryTag               = "content-registry"
	contentFieldsProperty     = "fields"
	registryNamespaceProperty = "route_namespace"
)

func registryExample() registryRenderer {
	example := content.CollectionTemplate("events")
	return registryRenderer{TypeKey: example.Key, Namespace: example.Namespace, Available: true, Fields: registryFields(example.Fields)}
}

func registryFieldsSchema() *huma.Schema {
	const registryFieldDescription = "One field definition. New fields are identified by their keys and receive server-generated IDs."
	field := responseDTOSchema(registryFieldDescription, registryExample().Fields[0])
	field.AdditionalProperties = false
	field.Properties["config"] = &huma.Schema{Type: huma.TypeObject, Description: "Kind-specific validators and enum choices."}
	field.Properties["display_labels"] = &huma.Schema{Type: huma.TypeObject, Description: "Locale-keyed plain-text labels and help."}
	return &huma.Schema{Type: huma.TypeArray, Items: field, MinItems: new(1), MaxItems: new(64)}
}

func addRegistrySchemas(schemas map[string]*huma.Schema) {
	const rendererSchema = "CollectionRenderer"
	option := registryExample()
	schema := responseDTOSchema("Registered renderer and whether its collection can be created.", option)
	schema.Properties[contentFieldsProperty] = registryFieldsSchema()
	schemas[rendererSchema] = schema
	options := responseDTOSchema("Renderer choices for the installation's collection registry.", registryRenderersResponse{Items: []registryRenderer{option}})
	options.Properties[privatePageItemsField] = &huma.Schema{Type: huma.TypeArray, Items: schemaRef(rendererSchema)}
	schemas[registryRenderersSchema] = options
	managedFields := registryFieldsSchema()
	managedFields.Items.Properties["field_id"] = uuidSchema(exampleUUID)
	managedFields.Items.Required = append(managedFields.Items.Required, "field_id")
	exampleFields := make([]managedRegistryField, 0, len(option.Fields))
	for _, field := range option.Fields {
		exampleFields = append(exampleFields, managedRegistryField{FieldID: exampleUUID, registryField: field})
	}
	grantExample := registryAccessGrant{Operation: string(identity.OpContentRead), Scope: roleAssignedObjects, FieldIDs: []string{exampleUUID}}
	receipt := responseDTOSchema("Exact non-delegable self-access applied by this committed write. Omitted on reads and writes without opt-in.", registryAccessReceipt{
		RoleID: exampleUUID, Grants: []registryAccessGrant{grantExample},
	})
	receipt.Properties["grants"] = &huma.Schema{Type: huma.TypeArray, Items: responseDTOSchema("The operation, object scope and exact field identifiers applied by this command.", grantExample)}
	schemas[registryCollectionSchema] = closedObjectSchema("Current schema and optimistic version for a managed collection.",
		map[string]*huma.Schema{
			typeIDField: uuidSchema(exampleUUID), typeKeyField: contentTypeParameter("").Schema,
			registryNamespaceProperty: stringSchema("Registered public renderer namespace.", option.Namespace),
			draftVersionField:         contentVersionSchema("Collection version used for the next revision.", "1"),
			schemaNoField:             contentVersionSchema("Current sealed semantic schema number.", "1"),
			contentFieldsProperty:     managedFields,
			"applied_access":          receipt,
		},
		[]string{typeIDField, typeKeyField, registryNamespaceProperty, draftVersionField, schemaNoField, contentFieldsProperty},
		registryCollectionResponse{TypeID: exampleUUID, TypeKey: option.TypeKey, Namespace: option.Namespace, Version: 1, SchemaNo: 1, Fields: exampleFields},
	)
}

var registryWriteContracts = map[string]*huma.Schema{
	registryCreateOperation: registryWriteSchema(true),
	registryReviseOperation: registryWriteSchema(false),
}

func registryWriteSchema(create bool) *huma.Schema {
	option := registryExample()
	properties := map[string]*huma.Schema{contentFieldsProperty: registryFieldsSchema()}
	required := []string{contentFieldsProperty}
	var example any
	if create {
		namespace := stringSchema("Namespace of the registered renderer to use.", option.Namespace)
		namespace.Pattern = "^[a-z][a-z0-9-]{0,63}$"
		properties[registryNamespaceProperty] = namespace
		properties["initial_access"] = &huma.Schema{Type: huma.TypeBoolean, Description: "Request initial access to the fresh collection."}
		required = append(required, registryNamespaceProperty)
		example = registryCreateInput{Namespace: option.Namespace, InitialAccess: true, Fields: option.Fields}
	} else {
		properties[expectedVersionField] = contentVersionSchema("Current collection version.", "1")
		properties["initialize_new_field_access"] = &huma.Schema{Type: huma.TypeBoolean, Description: "Request exact grants for newly created optional fields."}
		required = append(required, expectedVersionField)
		example = registryReviseInput{ExpectedVersion: 1, Fields: option.Fields}
	}
	return closedObjectSchema("Bounded Registry write input. Submit every field in the intended schema.", properties, required, example)
}

func registryRequestBody(registry huma.Registry, operation string) *huma.RequestBody {
	name := operation + "Request"
	schema := registryWriteContracts[operation]
	registry.Map()[name] = schema
	return &huma.RequestBody{Required: true, Description: "One JSON object up to 262144 bytes. Duplicate and unknown keys and null properties are rejected.",
		Content: map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(name), Example: schema.Examples[0]}}}
}

func registryErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{
		400: {validationCode}, 401: {authenticationCode}, 403: {originCode, permissionCode},
		404: {notFoundCode}, 405: {methodNotAllowedCode}, 503: {securityUnavailableCode},
	}
	if rt.method != http.MethodGet {
		codes[403] = append(codes[403], csrfCode, stepUpRequiredCode)
		codes[409] = []string{versionConflictCode, "invalid_transition"}
		if rt.operationID == registryCreateOperation {
			codes[409] = append(codes[409], collectionExistsCode)
		}
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	return codes
}
