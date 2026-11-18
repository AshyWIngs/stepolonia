package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

type userRolesReplacer interface {
	ReplaceUserRoles(context.Context, identity.AuthRequest, string, int64, []string) (identity.User, error)
}

var _ userRolesReplacer = (*identity.AuthService)(nil)

const userRolesPath = userPath + "/roles"
const replaceUserRolesOperation = "usersReplaceRoles"
const roleIDsField = "role_ids"

type replaceUserRolesInput struct {
	ExpectedVersion int64    `json:"expected_version"`
	RoleIDs         []string `json:"role_ids"`
}

var replaceUserRolesRoute = route{
	kind: fullKind, fields: requestFields(replaceUserRolesInput{}), method: http.MethodPut,
	operationID: replaceUserRolesOperation, summary: "Replace an account's role memberships", response: safeUserSchema,
	description: "Requires current roles.assign permission, a full session, CSRF and strong authentication no older than five minutes. The server checks account visibility, current and requested role policies, and effective and latent access under the shared security mutex. Superior targets are hidden. Pending accounts cannot receive roles before enrollment. The replacement preserves lifecycle states, prevents self-escalation and preserves a recoverable access administrator. Memberships, account version and audit commit atomically.",
}

func (h *handler) serveUserRoles(w http.ResponseWriter, r *http.Request, rt route, id, ip string) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	input, code, status := readUserRolesInput(w, r, rt)
	if code != "" {
		problem(w, status, code)
		return
	}
	service, ok := h.service.(userRolesReplacer)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	user, err := service.ReplaceUserRoles(r.Context(), req, id, input.ExpectedVersion, input.RoleIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, safeUser(user))
}

func readUserRolesInput(w http.ResponseWriter, r *http.Request, rt route) (replaceUserRolesInput, string, int) {
	var input replaceUserRolesInput
	code, status := readReplacementInput(w, r, rt, &input)
	if code == "" && (input.ExpectedVersion <= 0 || input.RoleIDs == nil || slices.Contains(input.RoleIDs, "")) {
		code, status = validationCode, http.StatusBadRequest
	}
	return input, code, status
}

func replacementRequest(w http.ResponseWriter, r *http.Request, rt route, ip string) (identity.AuthRequest, bool) {
	return requestWithID(w, r, rt.kind, ip)
}

func requestWithID(w http.ResponseWriter, r *http.Request, kind, ip string) (identity.AuthRequest, bool) {
	req, _, err := requestSession(r, kind, ip)
	if err != nil {
		writeError(w, err)
		return identity.AuthRequest{}, false
	}
	ok := attachRequestID(w, &req)
	return req, ok
}

func readReplacementInput(w http.ResponseWriter, r *http.Request, rt route, input any) (string, int) {
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		return code, status
	}
	if !allowedObject(raw, rt.fields, rt.operationID == replaceContentAssigneesOperation) || json.Unmarshal(raw, input) != nil {
		return validationCode, http.StatusBadRequest
	}
	return "", 0
}

func userRolesRequestSchema(rt route) (*huma.Schema, any) {
	version := credentialProperty(rt, expectedVersionField)
	version.Description = "Positive current account version checked under the shared security mutex before the replacement."
	items := uuidSchema(exampleUUID)
	items.Description = "Stable UUID of an existing role."
	roles := &huma.Schema{
		Type: huma.TypeArray, Items: items, MaxItems: intPointer(identity.MaxUserRoles), UniqueItems: true,
		Description: "Complete role membership set. Repeated UUIDs are rejected regardless of case. An empty array removes all memberships.",
	}
	example := replaceUserRolesInput{ExpectedVersion: 2, RoleIDs: []string{exampleUUID}}
	schema := closedObjectSchema(
		"Complete replacement of role memberships at the supplied account version. Both properties are required and role_ids may be empty.",
		map[string]*huma.Schema{expectedVersionField: version, roleIDsField: roles}, []string{expectedVersionField, roleIDsField}, example,
	)
	schema.Examples = append(schema.Examples, replaceUserRolesInput{ExpectedVersion: 3, RoleIDs: []string{}})
	return schema, example
}
