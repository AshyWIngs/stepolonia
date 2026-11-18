package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const (
	readySharedOperation = "contentMarkSharedReady"
	readyLocaleOperation = "contentMarkReady"
	publishOperation     = "contentPublish"
	adoptSchemaOperation = "contentAdoptDraftSchema"
	unpublishOperation   = "contentUnpublish"
	readySharedPath      = contentDraftPath + "/ready-shared"
	readyLocalePath      = contentDraftPath + "/ready"
	publishPath          = contentDraftPath + "/publish"
	adoptSchemaPath      = contentDraftPath + "/adopt-schema"
	unpublishPath        = contentDraftPath + "/unpublish"
	publishedDraftSchema = "PublishedContentDraft"
	candidateLocaleField = "locale"
	intendedLocalesField = "intended_locales"
)

var draftCandidateRoutes = map[string]route{
	readySharedPath: candidateRoute(readySharedOperation, "Mark the shared draft ready", "Validates shared values and relations and approves the current shared sequence."),
	readyLocalePath: candidateRoute(readyLocaleOperation, "Mark a draft locale ready", "Approves the current locale, shared and schema versions and the enabled language generation."),
	publishPath:     publishDraftRoute(),
	adoptSchemaPath: candidateRoute(adoptSchemaOperation, "Adopt the current collection schema", "Requires update access to all target-schema fields. Keeps existing values and relations and resets readiness on adoption."),
	unpublishPath:   candidateRoute(unpublishOperation, "Remove a published content item", "Requires content.items.unpublish on the item. Clears public visibility and readiness. Preserves immutable history and route reservations. Accepts the current draft's pinned schema even when a newer schema exists."),
}

func publishDraftRoute() route {
	rt := candidateRoute(publishOperation, "Publish ready draft locales", "Publishes a sealed snapshot of the intended ready locales and retains eligible previously published locales. Incompatible retained locales must be approved again and included in the request.")
	rt.response = publishedDraftSchema
	return rt
}

func candidateRoute(operation, summary, detail string) route {
	return route{kind: fullKind, method: http.MethodPost, operationID: operation, summary: summary, response: contentDraftSchema,
		description: "Requires a verified draft database login, a full session, CSRF and strong authentication no older than five minutes. Rechecks the collection, item scope, required field permissions and expected versions under the shared security mutex. " + detail + " The result and its audit commit together."}
}

func draftCandidateTarget(path string) (routeTarget, route, bool) {
	slash := strings.LastIndexByte(path, '/')
	if slash < 0 {
		return routeTarget{}, route{}, false
	}
	base, ok := contentDraftTarget(path[:slash])
	if !ok {
		return routeTarget{}, route{}, false
	}
	base.path += path[slash:]
	rt, ok := draftCandidateRoutes[base.path]
	return base, rt, ok
}

type draftCandidateInput struct {
	ExpectedVersion int64    `json:"expected_version,string"`
	SchemaNo        int64    `json:"schema_no,string"`
	Locale          string   `json:"locale,omitempty"`
	IntendedLocales []string `json:"intended_locales,omitempty"`
}

func (h *handler) serveDraftCandidate(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	input, ok := parseDraftCandidateInput(raw, rt.operationID)
	if !ok {
		writeError(w, identity.ErrValidation)
		return
	}
	state, err := h.runDraftCandidate(r.Context(), req, rt.operationID, target, input)
	if err != nil {
		writeDraftCandidateError(w, rt.operationID, err)
		return
	}
	respond(w, draftResponse(state))
}

func (h *handler) runDraftCandidate(ctx context.Context, req identity.AuthRequest, operation string, target routeTarget, input draftCandidateInput) (content.DraftState, error) {
	switch operation {
	case readySharedOperation:
		return h.draftEditor.MarkSharedReadyForType(ctx, req, target.typeKey, target.id, input.ExpectedVersion, input.SchemaNo)
	case readyLocaleOperation:
		return h.draftEditor.MarkReadyForType(ctx, req, target.typeKey, target.id, input.Locale, input.ExpectedVersion, input.SchemaNo)
	case publishOperation:
		return h.draftEditor.PublishForType(ctx, req, target.typeKey, target.id, input.ExpectedVersion, input.SchemaNo, input.IntendedLocales)
	case adoptSchemaOperation:
		return h.draftEditor.AdoptSchemaForType(ctx, req, target.typeKey, target.id, input.ExpectedVersion, input.SchemaNo)
	case unpublishOperation:
		return h.draftEditor.UnpublishForType(ctx, req, target.typeKey, target.id, input.ExpectedVersion, input.SchemaNo)
	default:
		return content.DraftState{}, identity.ErrNotFound
	}
}

func parseDraftCandidateInput(raw []byte, operation string) (draftCandidateInput, bool) {
	var input draftCandidateInput
	value, ok := decodeUniqueJSON(raw)
	if !ok || !utf8.Valid(raw) {
		return input, false
	}
	schema := draftCandidateContracts[operation]
	object, ok := value.(map[string]any)
	if !ok || schema == nil || !canonicalDraftVersions(object) || !exactDraftProperties(value, schema) {
		return input, false
	}
	var result huma.ValidateResult
	huma.Validate(nil, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	ok = len(result.Errors) == 0 && json.Unmarshal(raw, &input) == nil
	return input, ok
}

const (
	notReadyCode            = "not_ready"
	relationUnavailableCode = "relation_unavailable"
	localeUnavailableCode   = "locale_unavailable"
	routeConflictCode       = "route_conflict"
)

var draftCandidateConflicts = map[string][]string{
	readySharedOperation: {schemaConflictCode, versionConflictCode, relationUnavailableCode},
	readyLocaleOperation: {schemaConflictCode, versionConflictCode, localeUnavailableCode, relationUnavailableCode},
	publishOperation:     {schemaConflictCode, versionConflictCode, notReadyCode, localeUnavailableCode, routeConflictCode, relationUnavailableCode},
	adoptSchemaOperation: {schemaConflictCode, versionConflictCode},
	unpublishOperation:   {schemaConflictCode, versionConflictCode},
}

func writeDraftCandidateError(w http.ResponseWriter, operation string, err error) {
	var domain *content.Error
	if errors.As(err, &domain) && domain.Kind == content.Conflict && slices.Contains(draftCandidateConflicts[operation], domain.Code) {
		problem(w, http.StatusConflict, domain.Code)
		return
	}
	writeDraftError(w, err)
}
