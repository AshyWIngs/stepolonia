package adminhttp

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"stepolonia/apps/api/internal/identity"
)

const (
	createDraftPath      = adminContentPrefix + "{type_key}"
	createDraftOperation = "contentCreateDraft"
	createdDraftSchema   = "CreatedContentDraft"
)

var createDraftRoute = route{
	kind: fullKind, method: http.MethodPost, operationID: createDraftOperation,
	summary: "Create an empty content draft", response: createdDraftSchema,
	description: "Available only with a verified draft database login. Requires a full session, CSRF and content.items.create on the named collection. The current schema is checked under the shared security mutex. The item, empty draft, creator assignment and audit commit together. Each successful request creates a new item.",
}

func createDraftTarget(path string) (routeTarget, bool) {
	typeKey, ok := strings.CutPrefix(path, adminContentPrefix)
	if !ok || typeKey == "" || strings.Contains(typeKey, "/") {
		return routeTarget{}, false
	}
	return routeTarget{path: createDraftPath, typeKey: typeKey}, true
}

func (h *handler) serveDraftCreate(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	schemaNo, ok := parseCreateDraftInput(raw)
	if !ok {
		writeError(w, identity.ErrValidation)
		return
	}
	state, err := h.draftEditor.CreateDraftForType(r.Context(), req, target.typeKey, schemaNo)
	if err != nil {
		writeDraftError(w, err)
		return
	}
	w.Header().Set("Location", adminContentPrefix+url.PathEscape(target.typeKey)+"/"+url.PathEscape(state.ItemID))
	respondStatus(w, http.StatusCreated, newDraftReceipt(state))
}

func parseCreateDraftInput(raw []byte) (int64, bool) {
	value, ok := decodeUniqueJSON(raw)
	if !ok || !utf8.Valid(raw) {
		return 0, false
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return 0, false
	}
	schemaNo, ok := object[schemaNoField].(string)
	if !ok || !isContentVersion(schemaNo) {
		return 0, false
	}
	version, err := strconv.ParseInt(schemaNo, 10, 64)
	return version, err == nil
}
