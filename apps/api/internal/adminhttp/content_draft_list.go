package adminhttp

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const (
	listDraftsOperation    = "contentListDrafts"
	draftPageSchemaName    = "ContentDraftPage"
	draftSummarySchemaName = "ContentDraftSummary"
)

var listDraftsRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: listDraftsOperation,
	summary: "List accessible content drafts", response: draftPageSchemaName,
	description: "Requires a full session and current content.items.read permission on the named collection. The actor's item scope applies before pagination. Returns draft summaries in ascending item UUID order. Titles require a current field grant on the item. Requires a verified draft database login.",
}

func (h *handler) serveDraftList(w http.ResponseWriter, r *http.Request, target routeTarget, ip string) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, identity.ErrValidation)
		return
	}
	limit, offset, err := boundedPage(r.URL.RawQuery, identity.DefaultDraftPageLimit, identity.MaxDraftPageLimit, identity.MaxDraftPageOffset)
	if err != nil {
		writeError(w, err)
		return
	}
	req, _, err := requestSession(r, fullKind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	if !attachRequestID(w, &req) {
		return
	}
	page, err := h.draftEditor.ListDraftsForType(r.Context(), req, target.typeKey, limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, page)
}

func draftListSchemas() map[string]*huma.Schema {
	example := identity.DraftSummary{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 2,
		Titles: map[string]string{"pl": "Spacer po Ałmaty"}, PublishedLocales: []string{"pl"}}
	summary := closedObjectSchema("Identity, versions and available presentation for an authorized draft.", map[string]*huma.Schema{
		itemIDField: uuidSchema(exampleUUID), typeIDField: uuidSchema(exampleUUID),
		schemaNoField:     contentVersionSchema("Collection schema pinned to the draft.", "1"),
		draftVersionField: contentVersionSchema("Item version at the time the page was read.", "2"),
		"titles": {
			Type: huma.TypeObject, MaxProperties: intPointer(content.MaxDraftLocales),
			Description:          "Locale-keyed titles from the pinned draft schema available to the actor. Values are trimmed and shortened for the list. Omitted when no title is available.",
			AdditionalProperties: &huma.Schema{Type: huma.TypeString, MinLength: intPointer(1), MaxLength: intPointer(identity.MaxDraftSummaryTitleChars)},
		},
		"published_locales": {
			Type: huma.TypeArray, MaxItems: intPointer(content.MaxDraftLocales), UniqueItems: true,
			Description: "Locales with a currently visible public translation, in locale-code order. Omitted when there is no public translation.",
			Items:       &huma.Schema{Type: huma.TypeString},
		},
	}, []string{itemIDField, typeIDField, schemaNoField, draftVersionField}, example)
	page := privatePageSchema("Page of authorized draft summaries. next_offset is null when there is no following page within the supported range.",
		draftSummarySchemaName, identity.MaxDraftPageLimit, identity.MaxDraftPageOffset, identity.DraftPage{Items: []identity.DraftSummary{example}})
	return map[string]*huma.Schema{draftSummarySchemaName: summary, draftPageSchemaName: page}
}
