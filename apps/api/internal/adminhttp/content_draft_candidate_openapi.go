package adminhttp

import (
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
)

var draftCandidateContracts = map[string]*huma.Schema{
	readySharedOperation: draftCandidateSchema(draftCandidateInput{ExpectedVersion: 2, SchemaNo: 1}),
	readyLocaleOperation: draftCandidateSchema(draftCandidateInput{ExpectedVersion: 3, SchemaNo: 1, Locale: "pl"}),
	publishOperation:     draftCandidateSchema(draftCandidateInput{ExpectedVersion: 4, SchemaNo: 1, IntendedLocales: []string{"pl"}}),
	adoptSchemaOperation: draftTransitionSchema("The current collection schema to adopt.", draftCandidateInput{ExpectedVersion: 2, SchemaNo: 2}),
	unpublishOperation:   draftTransitionSchema("The draft's pinned schema number.", draftCandidateInput{ExpectedVersion: 5, SchemaNo: 1}),
}

func draftTransitionSchema(description string, example draftCandidateInput) *huma.Schema {
	schema := draftCandidateSchema(example)
	schema.Description = "Expected item version and collection schema for the versioned draft transition."
	schema.Properties[schemaNoField] = contentVersionSchema(description, strconv.FormatInt(example.SchemaNo, 10))
	schema.PrecomputeMessages()
	return schema
}

func draftCandidateSchema(example draftCandidateInput) *huma.Schema {
	properties := map[string]*huma.Schema{
		expectedVersionField: contentVersionSchema("Current item version.", "2"),
		schemaNoField:        contentVersionSchema("Current semantic schema pinned to the item.", "1"),
	}
	required := []string{expectedVersionField, schemaNoField}
	locale := stringSchema("Enabled language code in Registry form.", "pl")
	locale.Pattern = "^[a-z0-9]+(-[a-z0-9]+)*$"
	locale.MaxLength = new(64)
	switch {
	case example.Locale != "":
		required = append(required, candidateLocaleField)
		properties[candidateLocaleField] = locale
	case len(example.IntendedLocales) != 0:
		required = append(required, intendedLocalesField)
		properties[intendedLocalesField] = &huma.Schema{Type: huma.TypeArray, Items: locale, UniqueItems: true, MinItems: new(1), MaxItems: new(content.MaxDraftLocales), Description: "Enabled draft locales to update in the published snapshot."}
		properties[allocateSlugField] = &huma.Schema{Type: huma.TypeBoolean, Description: "Allocate a readable address from the draft slug, using a numbered suffix when the base is already reserved. The default preserves the exact draft slug and rejects conflicts."}
	}
	schema := closedObjectSchema("Item version and current semantic schema required to authorize this draft command.", properties, required, example)
	schema.PrecomputeMessages()
	return schema
}

func draftCandidateRequestBody(registry huma.Registry, operation string, schema *huma.Schema) *huma.RequestBody {
	name := operation + "Request"
	registry.Map()[name] = schema
	return &huma.RequestBody{Required: true,
		Description: "One JSON object, at most 4096 bytes. Versions are canonical positive int64 strings. Duplicate or unknown properties, null and trailing JSON are rejected.",
		Content:     map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(name), Example: schema.Examples[0]}},
	}
}

func publishedDraftResultSchema() *huma.Schema {
	schema := draftResultSchema()
	schema.Description = "Committed publication ID and draft identity, with actor-readable draft state when permitted."
	schema.Required = append(schema.Required, publicationIDField)
	example := schema.Examples[0].(savedDraftResponse)
	example.PublicationID = exampleUUID
	receipt := example
	receipt.readableDraft = nil
	schema.Examples = []any{example, receipt}
	return schema
}
