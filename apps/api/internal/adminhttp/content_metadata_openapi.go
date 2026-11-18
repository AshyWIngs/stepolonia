package adminhttp

import (
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

func addContentMetadataSchemas(schemas map[string]*huma.Schema) {
	const collectionSchema = "ReadableContentCollection"
	const fieldSchema = "ReadableDraftField"
	collection := identity.ReadableCollection{TypeID: exampleUUID, TypeKey: "article", SchemaNo: 2}
	schemas[collectionSchema] = closedObjectSchema("A collection available for draft browsing.", map[string]*huma.Schema{
		typeIDField:   uuidSchema(exampleUUID),
		typeKeyField:  contentTypeParameter("").Schema,
		schemaNoField: contentVersionSchema("Current sealed schema number.", "2"),
	}, []string{typeIDField, typeKeyField, schemaNoField}, collection)
	schemas[collectionPageSchemaName] = privatePageSchema(
		"Authorized collections ordered by collection key.", collectionSchema, identity.MaxDraftPageLimit, identity.MaxDraftPageOffset,
		identity.ReadableCollectionPage{Items: []identity.ReadableCollection{collection}},
	)

	field := content.DraftFieldMetadata{
		FieldID: exampleUUID, Key: "format", Kind: "enum", Scope: "shared", Visibility: "public", Required: true,
		Config: json.RawMessage(`{"codes":["meeting","workshop"]}`), DisplayLabels: json.RawMessage(`{"pl":{"label":"Format wydarzenia"}}`),
	}
	schemas[fieldSchema] = responseDTOSchema("A readable field with pinned constraints and current display text.", field)
	schemas[draftMetadataSchemaName] = closedObjectSchema(
		"Field definitions visible to the actor for the draft's pinned schema.",
		map[string]*huma.Schema{
			itemIDField:   uuidSchema(exampleUUID),
			schemaNoField: contentVersionSchema("Schema number pinned to the draft.", "1"),
			"fields":      {Type: huma.TypeArray, Items: schemaRef(fieldSchema)},
		}, []string{itemIDField, schemaNoField, "fields"},
		content.DraftMetadata{ItemID: exampleUUID, SchemaNo: 1, Fields: []content.DraftFieldMetadata{field}},
	)
}
