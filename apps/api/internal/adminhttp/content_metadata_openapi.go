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
	collection := identity.ReadableCollection{TypeID: exampleUUID, TypeKey: "article", SchemaNo: 2, CanCreate: true}
	schemas[collectionSchema] = closedObjectSchema("A collection available for draft browsing.", map[string]*huma.Schema{
		typeIDField:   uuidSchema(exampleUUID),
		typeKeyField:  contentTypeParameter("").Schema,
		schemaNoField: contentVersionSchema("Current sealed schema number.", "2"),
		"can_create":  {Type: huma.TypeBoolean, Description: "The current session may create drafts in this collection."},
	}, []string{typeIDField, typeKeyField, schemaNoField, "can_create"}, collection)
	schemas[collectionPageSchemaName] = privatePageSchema(
		"Authorized collections ordered by collection key.", collectionSchema, identity.MaxDraftPageLimit, identity.MaxDraftPageOffset,
		identity.ReadableCollectionPage{Items: []identity.ReadableCollection{collection}},
	)

	field := content.DraftFieldMetadata{
		FieldID: exampleUUID, Key: "format", Kind: "enum", Scope: "shared", Visibility: "public", Required: true, Editable: true,
		Config: json.RawMessage(`{"codes":["meeting","workshop"]}`), DisplayLabels: json.RawMessage(`{"pl":{"label":"Format wydarzenia"}}`),
	}
	schemas[fieldSchema] = responseDTOSchema("A readable field with pinned constraints and current display text.", field)
	const currentSchemaField = "current_schema_no"
	schemas[draftMetadataSchemaName] = closedObjectSchema(
		"Field definitions visible to the actor for the draft's pinned schema.",
		map[string]*huma.Schema{
			itemIDField:        uuidSchema(exampleUUID),
			schemaNoField:      contentVersionSchema("Schema number pinned to the draft.", "1"),
			draftVersionField:  contentVersionSchema("Item version for these metadata and publication links.", "2"),
			currentSchemaField: contentVersionSchema("Latest sealed schema for the collection.", "2"),
			"fields":           {Type: huma.TypeArray, Items: schemaRef(fieldSchema)},
			"actions":          responseDTOSchema("Permissions for the draft controls. Write commands always recheck authorization.", content.DraftActions{Ready: true}),
			"published": {Type: huma.TypeArray, Items: responseDTOSchema("A language and link in the current public snapshot.", content.PublicTranslation{
				Language: "pl", Slug: "warsztaty", CanonicalURL: "/pl/articles/warsztaty",
			})},
			"ready_locales": {Type: huma.TypeArray, Items: stringSchema("Language code with current readiness."), Description: "Enabled locales approved for the draft's current sequences, schema and language generation."},
		}, []string{itemIDField, schemaNoField, draftVersionField, currentSchemaField, "fields", "actions", "published", "ready_locales"},
		content.DraftMetadata{ItemID: exampleUUID, SchemaNo: 1, Version: 2, CurrentSchemaNo: 2, Fields: []content.DraftFieldMetadata{field},
			Actions: content.DraftActions{Ready: true}, Published: []content.PublicTranslation{}, ReadyLocales: []string{}},
	)
}
