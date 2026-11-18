package cmshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/content"
)

const (
	contentDraftPath   = adminContentPrefix + "{type_key}/{item_id}"
	saveDraftOperation = "contentSaveDraft"
	schemaNoField      = "schema_no"
	schemaConflictCode = "schema_conflict"
	maxDraftBodyBytes  = 262144
)

// DraftEditor verifies the persisted item and current field permissions before editing.
type DraftEditor interface {
	SaveDraftForType(context.Context, cms.AuthRequest, string, string, int64, content.DraftPatch) (content.DraftState, error)
}

type saveDraftInput struct {
	ExpectedVersion int64                         `json:"expected_version,string"`
	SchemaNo        int64                         `json:"schema_no,string"`
	Shared          map[string]content.FieldValue `json:"shared,omitempty"`
	UnsetShared     []string                      `json:"unset_shared,omitempty"`
	Locales         map[string]draftLocalePatch   `json:"locales,omitempty"`
}

type draftLocalePatch struct {
	Values map[string]content.FieldValue `json:"values,omitempty"`
	Unset  []string                      `json:"unset,omitempty"`
}

var saveDraftRoute = route{
	kind: fullKind, method: http.MethodPatch, operationID: saveDraftOperation,
	summary: "Save scalar content draft changes", response: contentDraftSchema,
	description: "Available only when the installation configures and verifies its separate draft database login. Requires a full session, CSRF and strong authentication no older than five minutes. Checks the item's actual collection, scope, each touched field and the expected versions in one transaction. Accepts scalar changes only. Changes and their audit commit together.",
}

func contentDraftTarget(path string) (routeTarget, bool) {
	rest, ok := strings.CutPrefix(path, adminContentPrefix)
	if !ok {
		return routeTarget{}, false
	}
	typeKey, itemID, ok := strings.Cut(rest, "/")
	if !ok || typeKey == "" || itemID == "" || strings.Contains(itemID, "/") {
		return routeTarget{}, false
	}
	return routeTarget{path: contentDraftPath, id: itemID, typeKey: typeKey}, true
}

func (h *handler) serveDraftSave(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	input, code, status := readDraftInput(w, r)
	if code != "" {
		problem(w, status, code)
		return
	}
	state, err := h.draftEditor.SaveDraftForType(r.Context(), req, target.typeKey, target.id, input.ExpectedVersion, input.patch())
	if err != nil {
		writeDraftError(w, err)
		return
	}
	respond(w, draftResponse(state))
}

func (input saveDraftInput) patch() content.DraftPatch {
	locales := make(map[string]content.ValuePatch, len(input.Locales))
	for locale, patch := range input.Locales {
		locales[locale] = content.ValuePatch{Values: patch.Values, Unset: patch.Unset}
	}
	return content.DraftPatch{SchemaNo: input.SchemaNo, Shared: input.Shared, UnsetShared: input.UnsetShared, Locales: locales}
}

func readDraftInput(w http.ResponseWriter, r *http.Request) (saveDraftInput, string, int) {
	var input saveDraftInput
	raw, code, status := readJSONBody(w, r, maxDraftBodyBytes)
	if code != "" {
		return input, code, status
	}
	if !validDraftInput(raw, &input) {
		return input, validationCode, http.StatusBadRequest
	}
	return input, "", 0
}

func validDraftInput(raw []byte, input *saveDraftInput) bool {
	value, ok := decodeUniqueJSON(raw)
	if !ok || !utf8.Valid(raw) {
		return false
	}
	object, ok := value.(map[string]any)
	if !ok || !canonicalDraftVersions(object) || !exactDraftProperties(value, draftRequestContract) {
		return false
	}
	var result huma.ValidateResult
	huma.Validate(nil, draftRequestContract, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	return len(result.Errors) == 0 && json.Unmarshal(raw, input) == nil
}

// Huma accepts case variants of fixed properties. Select exact property names
// here so they cannot merge when the transport decodes into a Go struct.
func exactDraftProperties(value any, schema *huma.Schema) bool {
	if list, ok := value.([]any); ok {
		return exactDraftArray(list, schema)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return true
	}
	if schema.Type != huma.TypeObject {
		return false
	}
	for key, child := range object {
		property := schema.Properties[key]
		if property == nil {
			property, ok = schema.AdditionalProperties.(*huma.Schema)
			if !ok {
				return false
			}
		}
		if !exactDraftProperties(child, property) {
			return false
		}
	}
	return true
}

func exactDraftArray(list []any, schema *huma.Schema) bool {
	if schema.Type != huma.TypeArray || schema.Items == nil {
		return false
	}
	for _, item := range list {
		if !exactDraftProperties(item, schema.Items) {
			return false
		}
	}
	return true
}

func canonicalDraftVersions(object map[string]any) bool {
	for _, key := range []string{expectedVersionField, schemaNoField} {
		text, ok := object[key].(string)
		if !ok || !isContentVersion(text) {
			return false
		}
	}
	return true
}

func writeDraftError(w http.ResponseWriter, err error) {
	var domain *content.Error
	if !errors.As(err, &domain) {
		writeError(w, err)
		return
	}
	switch domain.Kind {
	case content.Validation:
		problem(w, http.StatusBadRequest, validationCode)
	case content.Conflict:
		if domain.Code == schemaConflictCode {
			problem(w, http.StatusConflict, schemaConflictCode)
			return
		}
		problem(w, http.StatusConflict, versionConflictCode)
	case content.NotFound:
		writeError(w, cms.ErrNotFound)
	case content.Forbidden:
		writeError(w, cms.ErrPermissionDenied)
	default:
		writeError(w, cms.ErrSecurityUnavailable)
	}
}
