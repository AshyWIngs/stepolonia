package adminhttp

import (
	"net/http"
	"strings"

	"stepolonia/apps/api/internal/identity"
)

const (
	contentCollectionsPath     = "/api/v1/admin/content-types"
	draftMetadataPath          = contentDraftPath + draftMetadataSuffix
	draftMetadataSuffix        = "/schema"
	listCollectionsOperation   = "contentListReadableTypes"
	readDraftMetadataOperation = "contentGetDraftSchema"
	collectionPageSchemaName   = "ReadableContentCollectionPage"
	draftMetadataSchemaName    = "ReadableDraftSchema"
)

var listCollectionsRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: listCollectionsOperation,
	summary: "List collections available for draft browsing", response: collectionPageSchemaName,
	description: "Returns collections covered by the current full session's active content.items.read grants. An assigned-scope collection may contain no drafts assigned to the actor. Requires a verified draft database login.",
}

var readDraftMetadataRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: readDraftMetadataOperation,
	summary: "Read the draft field definitions", response: draftMetadataSchemaName,
	description: "Returns readable fields from the item's pinned schema with current labels and help text. Requires a full session with current collection, item and field access and a verified draft database login. Send no query parameters or body.",
}

func contentMetadataTarget(path string) (routeTarget, route, bool) {
	if path == contentCollectionsPath {
		return routeTarget{path: path}, listCollectionsRoute, true
	}
	base, ok := strings.CutSuffix(path, draftMetadataSuffix)
	if !ok {
		return routeTarget{}, route{}, false
	}
	target, ok := contentDraftTarget(base)
	target.path = draftMetadataPath
	return target, readDraftMetadataRoute, ok
}

func (h *handler) serveContentMetadata(w http.ResponseWriter, r *http.Request, target routeTarget, ip string) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, identity.ErrValidation)
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
	if target.path == contentCollectionsPath {
		h.serveReadableCollections(w, r, req)
		return
	}
	metadata, err := h.draftEditor.ReadDraftMetadata(r.Context(), req, target.typeKey, target.id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, metadata)
}

func (h *handler) serveReadableCollections(w http.ResponseWriter, r *http.Request, req identity.AuthRequest) {
	limit, offset, err := boundedPage(r.URL.RawQuery, identity.DefaultDraftPageLimit, identity.MaxDraftPageLimit, identity.MaxDraftPageOffset)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := h.draftEditor.ListReadableCollections(r.Context(), req, limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, page)
}
