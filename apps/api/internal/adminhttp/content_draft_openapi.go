package adminhttp

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
)

const (
	contentDraftTag     = "content-drafts"
	contentDraftSchema  = "SavedContentDraft"
	readableDraftSchema = "ReadableContentDraft"
	draftSharedField    = "shared"
	draftLocalesField   = "locales"
	typeIDField         = "type_id"
	draftVersionField   = "version"
	publicationIDField  = "publication_id"
	draftValuesField    = "values"
	localSequenceField  = "local_seq"
	sharedSequenceField = "shared_seq"
	draftExampleField   = "headline"
)

type draftReceipt struct {
	ItemID   string `json:"item_id"`
	TypeID   string `json:"type_id"`
	SchemaNo int64  `json:"schema_no,string"`
	Version  int64  `json:"version,string"`
}

type savedDraftResponse struct {
	draftReceipt
	PublicationID string `json:"publication_id,omitempty"`
	*readableDraft
}

type readableDraft struct {
	Shared         content.ScalarValues           `json:"shared"`
	SharedSeq      int64                          `json:"shared_seq,string"`
	SharedReadySeq *string                        `json:"shared_ready_seq"`
	Locales        map[string]readableDraftLocale `json:"locales"`
	Relations      []readableDraftRelation        `json:"relations"`
}

type readableDraftLocale struct {
	Values   content.ScalarValues `json:"values"`
	LocalSeq int64                `json:"local_seq,string"`
	Ready    *draftReady          `json:"ready"`
}

type draftReady struct {
	LocalSeq         int64 `json:"local_seq,string"`
	SharedSeq        int64 `json:"shared_seq,string"`
	SchemaNo         int64 `json:"schema_no,string"`
	LocaleGeneration int64 `json:"locale_generation,string"`
}

type readableDraftRelation struct {
	FieldID      string `json:"field_id"`
	Locale       string `json:"locale"`
	TargetTypeID string `json:"target_type_id"`
	TargetItemID string `json:"target_item_id"`
}

func newDraftReceipt(state content.DraftState) draftReceipt {
	return draftReceipt{ItemID: state.ItemID, TypeID: state.TypeID, SchemaNo: state.SchemaNo, Version: state.Version}
}

func draftResponse(state content.DraftState) savedDraftResponse {
	out := savedDraftResponse{draftReceipt: newDraftReceipt(state), PublicationID: state.PublicationID}
	// A positive shared sequence identifies a readable projection. The service
	// returns the identity and version only when the actor lacks item read access.
	if state.SharedSeq == 0 {
		return out
	}
	projection := &readableDraft{
		Shared: nonnullScalarValues(state.Shared), SharedSeq: state.SharedSeq,
		Locales: make(map[string]readableDraftLocale, len(state.Locales)), Relations: make([]readableDraftRelation, len(state.Relations)),
	}
	if state.SharedReadySeq.Valid {
		seq := strconv.FormatInt(state.SharedReadySeq.Int64, 10)
		projection.SharedReadySeq = &seq
	}
	for locale, draft := range state.Locales {
		projection.Locales[locale] = readableDraftLocale{Values: nonnullScalarValues(draft.Values), LocalSeq: draft.LocalSeq, Ready: (*draftReady)(draft.Ready)}
	}
	for i, relation := range state.Relations {
		projection.Relations[i] = readableDraftRelation(relation)
	}
	out.readableDraft = projection
	return out
}

func nonnullScalarValues(values content.ScalarValues) content.ScalarValues {
	if values == nil {
		return content.ScalarValues{}
	}
	return values
}

func draftRequestExample() saveDraftInput {
	return saveDraftInput{ExpectedVersion: 2, SchemaNo: 1, Locales: map[string]draftLocalePatch{
		"en": {Values: map[string]content.FieldValue{draftExampleField: {Kind: "string", Value: json.RawMessage(`"Spring workshops"`)}}},
	}}
}

var draftRequestContract = draftRequestSchema()

func draftRequestSchema() *huma.Schema {
	key := stringSchema("Stable field key from the item's pinned collection schema.")
	key.Pattern = `^[a-z][a-z0-9_]{0,63}$`
	unset := &huma.Schema{Type: huma.TypeArray, Items: key, UniqueItems: true, Description: "Field keys removed explicitly. Omitted changes preserve existing values."}
	kind := stringSchema("Scalar kind in the pinned schema. The value uses the codec for that kind.")
	kind.Enum = stringValues([]string{"string", "text", "markdown", "integer", "decimal", "datetime", "enum", "boolean"})
	field := closedObjectSchema("One typed scalar set. Boolean uses a JSON boolean. Other kinds use strings, including exact numeric values.",
		map[string]*huma.Schema{"kind": kind, "value": draftScalarSchema()}, []string{"kind", "value"}, nil)
	shared := draftValueMap("Scalar changes indexed by stable shared field key.", field)
	values := draftValueMap("Scalar changes indexed by stable localized field key.", field)
	locale := closedObjectSchema("Changes to a single locale.", map[string]*huma.Schema{draftValuesField: values, "unset": unset}, nil, nil)
	schema := closedObjectSchema("Versioned scalar patch. Kind, field scope and value codec are checked against the pinned schema.", map[string]*huma.Schema{
		expectedVersionField: contentVersionSchema("Current item version.", "2"),
		schemaNoField:        contentVersionSchema("Semantic schema pinned to the draft.", "1"),
		draftSharedField:     shared, "unset_shared": unset,
		draftLocalesField: draftValueMap("Localized changes indexed by normalized language code.", locale),
	}, []string{expectedVersionField, schemaNoField}, draftRequestExample())
	// Huma precomputes regular properties but does not traverse map values.
	field.PrecomputeMessages()
	locale.PrecomputeMessages()
	schema.PrecomputeMessages()
	return schema
}

func draftScalarSchema() *huma.Schema {
	return &huma.Schema{AnyOf: []*huma.Schema{{Type: huma.TypeString}, {Type: huma.TypeBoolean}}}
}

func draftValueMap(description string, value *huma.Schema) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: value, Description: description}
}

func draftResultSchema() *huma.Schema {
	ready := closedObjectSchema("Exact approved readiness tuple for this locale.", map[string]*huma.Schema{
		localSequenceField:  contentVersionSchema("Approved locale sequence.", "2"),
		sharedSequenceField: contentVersionSchema("Approved shared sequence.", "1"),
		schemaNoField:       contentVersionSchema("Approved semantic schema.", "1"),
		"locale_generation": contentVersionSchema("Approved language generation.", "1"),
	}, []string{localSequenceField, sharedSequenceField, schemaNoField, "locale_generation"}, nil)
	ready.Nullable = true
	locale := closedObjectSchema("Actor-readable scalar values and locale readiness.", map[string]*huma.Schema{
		draftValuesField:   draftValueMap("Actor-readable scalar values.", draftScalarSchema()),
		localSequenceField: contentVersionSchema("Current locale sequence.", "2"),
		"ready":            ready,
	}, []string{draftValuesField, localSequenceField, "ready"}, nil)
	relation := closedObjectSchema("One actor-readable relation whose target is also readable.", map[string]*huma.Schema{
		"field_id": uuidSchema(exampleUUID), "locale": stringSchema("Language code for a localized relation or empty for a shared relation.", "en"),
		"target_type_id": uuidSchema(exampleUUID), "target_item_id": uuidSchema(exampleUUID),
	}, []string{"field_id", "locale", "target_type_id", "target_item_id"}, nil)
	readySeq := contentVersionSchema("Approved shared sequence or null until approved.", "1")
	readySeq.Nullable = true
	example := savedDraftResponse{draftReceipt: draftReceipt{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 3}, readableDraft: &readableDraft{
		Shared: content.ScalarValues{}, SharedSeq: 1, Relations: []readableDraftRelation{},
		Locales: map[string]readableDraftLocale{"en": {Values: content.ScalarValues{draftExampleField: json.RawMessage(`"Spring workshops"`)}, LocalSeq: 2}},
	}}
	schema := closedObjectSchema("Committed draft identity and version. Readable draft state is included only for actors with item read access.", map[string]*huma.Schema{
		itemIDField: uuidSchema(exampleUUID), typeIDField: uuidSchema(exampleUUID),
		schemaNoField:       contentVersionSchema("Semantic schema pinned to the draft.", "1"),
		draftVersionField:   contentVersionSchema("Committed item version; use it for the next edit.", "3"),
		draftSharedField:    draftValueMap("Actor-readable shared field values.", draftScalarSchema()),
		sharedSequenceField: contentVersionSchema("Current shared sequence.", "1"), "shared_ready_seq": readySeq,
		draftLocalesField:  draftValueMap("Actor-readable localized drafts indexed by language code.", locale),
		"relations":        {Type: huma.TypeArray, Items: relation},
		publicationIDField: uuidSchema(exampleUUID),
	}, []string{itemIDField, typeIDField, schemaNoField, draftVersionField}, example)
	schema.Examples = append(schema.Examples, example.draftReceipt)
	return schema
}

func createDraftRequestBody(registry huma.Registry) *huma.RequestBody {
	name := createDraftOperation + "Request"
	example := map[string]string{schemaNoField: "1"}
	registry.Map()[name] = closedObjectSchema("Schema revision to pin to the empty draft.", map[string]*huma.Schema{
		schemaNoField: contentVersionSchema("Current collection schema checked under the shared security mutex.", "1"),
	}, []string{schemaNoField}, example)
	return &huma.RequestBody{
		Required: true, Description: "One JSON object, at most 4096 bytes. Provide only schema_no as a canonical positive int64 string. Duplicate or unknown properties, null and trailing JSON are rejected.",
		Content: map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(name), Example: example}},
	}
}

func createdDraftResultSchema() *huma.Schema {
	return closedObjectSchema("Identity and initial version of a committed empty draft.", map[string]*huma.Schema{
		itemIDField: uuidSchema(exampleUUID), typeIDField: uuidSchema(exampleUUID),
		schemaNoField:     contentVersionSchema("Current collection schema pinned to the new draft.", "1"),
		draftVersionField: contentVersionSchema("Initial item version; use it for the first edit.", "1"),
	}, []string{itemIDField, typeIDField, schemaNoField, draftVersionField},
		draftReceipt{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 1})
}

func draftErrorCodes(rt route) map[int][]string {
	if conflicts, ok := draftCandidateConflicts[rt.operationID]; ok {
		codes := draftErrorCodes(saveDraftRoute)
		codes[http.StatusConflict] = conflicts
		return codes
	}
	if rt.operationID == createDraftOperation {
		return map[int][]string{
			400: {validationCode}, 401: {authenticationCode}, 403: {originCode, csrfCode, stepUpRequiredCode},
			404: {notFoundCode}, 405: {methodNotAllowedCode}, 409: {schemaConflictCode},
			413: {bodyTooLargeCode}, 429: {rateLimitedCode}, 503: {securityUnavailableCode},
		}
	}
	if rt.method == http.MethodGet {
		return map[int][]string{
			400: {validationCode}, 401: {authenticationCode}, 403: {originCode}, 404: {notFoundCode},
			405: {methodNotAllowedCode}, 503: {securityUnavailableCode},
		}
	}
	return map[int][]string{
		400: {validationCode}, 401: {authenticationCode}, 403: {originCode, csrfCode, permissionCode, stepUpRequiredCode},
		404: {notFoundCode}, 405: {methodNotAllowedCode}, 409: {versionConflictCode, schemaConflictCode},
		413: {bodyTooLargeCode}, 429: {rateLimitedCode}, 503: {securityUnavailableCode},
	}
}

func draftReadSchema() *huma.Schema {
	schema := draftResultSchema()
	schema.Description = "The current draft identity, version and actor-readable projection."
	schema.Required = append(schema.Required, draftSharedField, sharedSequenceField, "shared_ready_seq", draftLocalesField, "relations")
	schema.Examples = schema.Examples[:1]
	return schema
}

func draftSuccessDescription(operationID string) string {
	switch operationID {
	case listDraftsOperation:
		return "Returns a bounded page of draft identities and versions authorized by collection and item scope."
	case createDraftOperation:
		return "Item, empty draft, creator assignment and audit committed. Returns the new item and collection IDs and the draft version."
	case readySharedOperation, readyLocaleOperation:
		return "Readiness and audit committed. Returns the current item version and any actor-readable draft state."
	case publishOperation:
		return "Publication and audit committed. Returns the current item version, the new publication ID and any actor-readable draft state."
	case readDraftOperation:
		return "Returns the current draft version and actor-readable state."
	default:
		return "Draft and audit committed. Returns the current item version and any actor-readable draft state."
	}
}
