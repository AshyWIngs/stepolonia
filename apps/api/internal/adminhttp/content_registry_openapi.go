package adminhttp

import (
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const (
	registryTag                = "content-registry"
	contentFieldsProperty      = "fields"
	registryNamespaceProperty  = "route_namespace"
	registryDisplayNameField   = "display_name"
	registryEnabledField       = "enabled"
	collectionIconField        = "icon_svg"
	defaultIconDescription     = "An empty string uses the default icon."
	collectionIconVersionField = "icon_version"
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
	summary := content.CollectionSummary{TypeID: exampleUUID, TypeKey: option.TypeKey, Enabled: true}
	options := responseDTOSchema("Managed collections and available creation templates.", registryRenderersResponse{
		Items: []registryRenderer{option}, Collections: []content.CollectionSummary{summary},
	})
	options.Properties[privatePageItemsField] = &huma.Schema{Type: huma.TypeArray, Items: schemaRef(rendererSchema)}
	summarySchema := responseDTOSchema("Persisted collection summary.", summary)
	summarySchema.Properties[collectionIconField] = collectionIconSchema(defaultIconDescription)
	options.Properties["collections"] = &huma.Schema{Type: huma.TypeArray, Items: summarySchema}
	schemas[registryRenderersSchema] = options
	managedFields := registryFieldsSchema()
	managedFields.Items.Properties["field_id"] = uuidSchema(exampleUUID)
	managedFields.Items.Properties["label_version"] = registryScopeVersionSchema("Version of the saved display labels.")
	managedFields.Items.Required = append(managedFields.Items.Required, "field_id", "label_version")
	exampleFields := make([]managedRegistryField, 0, len(option.Fields))
	for _, field := range option.Fields {
		exampleFields = append(exampleFields, managedRegistryField{FieldID: exampleUUID, LabelVersion: content.FieldLabelVersion(nil), registryField: field})
	}
	grantExample := registryAccessGrant{Operation: string(identity.OpContentRead), Scope: roleAssignedObjects, FieldIDs: []string{exampleUUID}}
	receipt := responseDTOSchema("Exact non-delegable self-access applied by this committed write. Omitted on reads and writes without opt-in.", registryAccessReceipt{
		RoleID: exampleUUID, Grants: []registryAccessGrant{grantExample},
	})
	receipt.Properties["grants"] = &huma.Schema{Type: huma.TypeArray, Items: responseDTOSchema("The operation, object scope and exact field identifiers applied by this command.", grantExample)}
	schemas[registryCollectionSchema] = closedObjectSchema("Current schema and optimistic version for a managed collection.",
		map[string]*huma.Schema{
			typeIDField: uuidSchema(exampleUUID), typeKeyField: contentTypeParameter("").Schema,
			registryNamespaceProperty:  stringSchema("Registered public renderer namespace.", option.Namespace),
			registryDisplayNameField:   collectionDisplayNameSchema(),
			collectionIconField:        collectionIconSchema(defaultIconDescription),
			collectionIconVersionField: collectionIconVersionSchema(),
			registryEnabledField:       {Type: huma.TypeBoolean, Description: "Whether the collection is available for authoring."},
			draftVersionField:          contentVersionSchema("Collection version used for the next revision.", "1"),
			schemaNoField:              contentVersionSchema("Current sealed semantic schema number.", "1"),
			contentFieldsProperty:      managedFields,
			"applied_access":           receipt,
		},
		[]string{typeIDField, typeKeyField, registryNamespaceProperty, registryDisplayNameField, collectionIconField, collectionIconVersionField, registryEnabledField, draftVersionField, schemaNoField, contentFieldsProperty},
		registryCollectionResponse{TypeID: exampleUUID, TypeKey: option.TypeKey, Namespace: option.Namespace, IconVersion: content.CollectionIconVersion(""), Enabled: true, Version: 1, SchemaNo: 1, Fields: exampleFields},
	)
	schemas[registryIconSchema] = collectionIconResponseSchema()
}

func collectionIconVersionSchema() *huma.Schema {
	return registryScopeVersionSchema("Version of the saved navigation icon.")
}

func registryScopeVersionSchema(description string) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: scopeVersionPattern, Description: description}
}

func collectionIconResponseSchema() *huma.Schema {
	const previousVersion = "previous_version"
	return closedObjectSchema("Committed change to the navigation icon.", map[string]*huma.Schema{
		previousVersion:            contentVersionSchema("Collection version before the change.", "1"),
		draftVersionField:          contentVersionSchema("Collection version after the change.", "2"),
		collectionIconField:        collectionIconSchema(defaultIconDescription),
		collectionIconVersionField: collectionIconVersionSchema(),
	}, []string{previousVersion, draftVersionField, collectionIconField, collectionIconVersionField},
		registryIconResponse{PreviousVersion: 1, Version: 2, IconVersion: content.CollectionIconVersion("")})
}

func collectionDisplayNameSchema() *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Description: "Collection name. Empty for built-in collections with translated interface labels.", MaxLength: new(content.MaxCollectionNameLength)}
}

func collectionIconSchema(description string) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString,
		Description: fmt.Sprintf("Static SVG navigation icon with a viewBox, up to %d UTF-8 bytes. %s", content.MaxCollectionIconBytes, description),
		MaxLength:   new(content.MaxCollectionIconBytes)}
}

var registryWriteContracts = map[string]*huma.Schema{
	registryCreateOperation:       registryWriteSchema(true),
	registryReviseOperation:       registryWriteSchema(false),
	registryFieldChangesOperation: registryFieldChangesSchema(),
	registryIconOperation: closedObjectSchema("Set the navigation icon independently of the field schema.", map[string]*huma.Schema{
		expectedVersionField: collectionIconVersionSchema(),
		collectionIconField:  collectionIconSchema(defaultIconDescription),
	}, []string{expectedVersionField, collectionIconField}, registryIconInput{ExpectedVersion: content.CollectionIconVersion("")}),
	registryAvailabilityOperation: closedObjectSchema("Change collection availability for authoring.", map[string]*huma.Schema{
		expectedVersionField: contentVersionSchema("Current collection version.", "1"),
		registryEnabledField: {Type: huma.TypeBoolean, Description: "Whether authors can open or edit drafts in this collection."},
	}, []string{expectedVersionField, registryEnabledField}, registryAvailabilityInput{ExpectedVersion: 1, Enabled: false}),
}

func registryWriteSchema(create bool) *huma.Schema {
	option := registryExample()
	properties := map[string]*huma.Schema{contentFieldsProperty: registryFieldsSchema()}
	required := []string{contentFieldsProperty}
	var example any
	if create {
		properties[collectionIconField] = collectionIconSchema("Omit or provide an empty string to use the default icon.")
		namespace := stringSchema("Namespace of the registered renderer to use.", option.Namespace)
		namespace.Pattern = "^[a-z][a-z0-9-]{0,63}$"
		properties[registryNamespaceProperty] = namespace
		properties[typeKeyField] = contentTypeParameter("Required for generic collections and must match the requested namespace.").Schema
		properties[registryDisplayNameField] = collectionDisplayNameSchema()
		properties["initial_access"] = &huma.Schema{Type: huma.TypeBoolean, Description: "Request initial access to the fresh collection."}
		required = append(required, registryNamespaceProperty)
		example = registryCreateInput{Namespace: option.Namespace, InitialAccess: true, Fields: option.Fields}
	} else {
		properties[collectionIconField] = collectionIconSchema("Omit to preserve the current icon. " + defaultIconDescription)
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
		codes[403] = append(codes[403], csrfCode)
		codes[409] = []string{versionConflictCode}
		if rt.operationID != registryIconOperation {
			codes[403] = append(codes[403], stepUpRequiredCode)
			codes[409] = append(codes[409], "invalid_transition")
		}
		if rt.operationID == registryCreateOperation || rt.operationID == registryReviseOperation || rt.operationID == registryIconOperation {
			codes[400] = append(codes[400], content.InvalidCollectionIconCode)
		}
		if rt.operationID == registryCreateOperation {
			codes[409] = append(codes[409], collectionExistsCode)
		}
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	return codes
}
