package cmshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"stepolonia/apps/api/internal/cms"
)

const (
	adminContentPrefix               = "/api/v1/admin/content/"
	assigneesSegment                 = "assignees"
	contentAssigneesPath             = adminContentPrefix + "{type_key}/{item_id}/" + assigneesSegment
	replaceContentAssigneesOperation = "contentReplaceAssignees"
	assigneeIDsField                 = "assignee_ids"
)

type contentAssigneesReplacer interface {
	ReplaceContentAssignees(context.Context, cms.AuthRequest, string, string, int64, []string) (cms.ContentAssignees, error)
}

var _ contentAssigneesReplacer = (*cms.AuthService)(nil)

type replaceContentAssigneesInput struct {
	ExpectedVersion int64    `json:"expected_version,string"`
	AssigneeIDs     []string `json:"assignee_ids"`
}

var replaceContentAssigneesRoute = route{
	kind: fullKind, fields: requestFields(replaceContentAssigneesInput{}), method: http.MethodPut,
	operationID: replaceContentAssigneesOperation, summary: "Replace a content item's assignees", response: contentAssigneesSchema,
	description: "Requires a full session, CSRF, strong authentication no older than five minutes and content.items.assign with any scope on the item's actual type. The server checks the type, item version, active recipients and the actor's delegation envelope under the shared security mutex. Adding oneself and increasing one's effective access are forbidden. Assignees, item version and audit commit atomically.",
}

func contentAssigneesTarget(path string) (routeTarget, bool) {
	keyAndItem, ok := strings.CutPrefix(path, adminContentPrefix)
	if !ok {
		return routeTarget{}, false
	}
	typeKey, itemAndAction, ok := strings.Cut(keyAndItem, "/")
	if !ok || typeKey == "" {
		return routeTarget{}, false
	}
	itemID, action, ok := strings.Cut(itemAndAction, "/")
	if !ok || itemID == "" || action != assigneesSegment {
		return routeTarget{}, false
	}
	return routeTarget{path: contentAssigneesPath, id: itemID, typeKey: typeKey}, true
}

func (h *handler) serveContentAssignees(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	input, code, status := readContentAssigneesInput(w, r, rt)
	if code != "" {
		problem(w, status, code)
		return
	}
	service, ok := h.service.(contentAssigneesReplacer)
	if !ok {
		writeError(w, cms.ErrSecurityUnavailable)
		return
	}
	result, err := service.ReplaceContentAssignees(r.Context(), req, target.typeKey, target.id, input.ExpectedVersion, input.AssigneeIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, contentAssigneesResponse{ItemID: result.ItemID, Version: result.Version, AssigneeIDs: result.AssigneeIDs})
}

func readContentAssigneesInput(w http.ResponseWriter, r *http.Request, rt route) (replaceContentAssigneesInput, string, int) {
	var input replaceContentAssigneesInput
	code, status := readReplacementInput(w, r, rt, &input)
	if code == "" && (input.ExpectedVersion <= 0 || input.AssigneeIDs == nil || len(input.AssigneeIDs) > cms.MaxContentAssignees || slices.Contains(input.AssigneeIDs, "")) {
		code, status = validationCode, http.StatusBadRequest
	}
	return input, code, status
}

func validContentVersion(raw json.RawMessage) bool {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return isContentVersion(value)
}

func isContentVersion(value string) bool {
	version, err := strconv.ParseInt(value, 10, 64)
	return err == nil && version > 0 && strconv.FormatInt(version, 10) == value
}
