package cmshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/cms"
)

type userRoleService interface {
	ReplaceUserRoles(context.Context, cms.AuthRequest, string, int64, []string) (cms.User, error)
}

var _ userRoleService = (*cms.AuthService)(nil)

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
	req, _, err := requestSession(r, rt.kind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	if !attachRequestID(w, &req) {
		return
	}
	input, code, status := readUserRolesInput(w, r, rt.fields)
	if code != "" {
		problem(w, status, code)
		return
	}
	service, ok := h.service.(userRoleService)
	if !ok {
		writeError(w, cms.ErrSecurityUnavailable)
		return
	}
	user, err := service.ReplaceUserRoles(r.Context(), req, id, input.ExpectedVersion, input.RoleIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, safeUser(user))
}

func readUserRolesInput(w http.ResponseWriter, r *http.Request, fields string) (replaceUserRolesInput, string, int) {
	var input replaceUserRolesInput
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		return input, code, status
	}
	if !allowedObject(raw, fields) || json.Unmarshal(raw, &input) != nil || input.ExpectedVersion <= 0 || input.RoleIDs == nil || slices.Contains(input.RoleIDs, "") {
		return input, validationCode, http.StatusBadRequest
	}
	return input, "", 0
}

func userRolesRequestSchema(rt route) (*huma.Schema, any) {
	version := credentialProperty(rt, expectedVersionField)
	version.Description = "Positive current account version checked under the shared security mutex before the replacement."
	items := uuidSchema(exampleUUID)
	items.Description = "Stable UUID of an existing role."
	roles := &huma.Schema{
		Type: huma.TypeArray, Items: items, MaxItems: intPointer(cms.MaxUserRoles), UniqueItems: true,
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
