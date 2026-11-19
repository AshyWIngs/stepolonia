package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

// RoleService supplies authorized role reads, policy writes and state changes.
type RoleService interface {
	ListRoles(context.Context, identity.AuthRequest, identity.RolePage) (identity.RoleList, error)
	GetRole(context.Context, identity.AuthRequest, string) (identity.Role, error)
	CreateRole(context.Context, identity.AuthRequest, identity.RoleInput) (identity.Role, error)
	EditRole(context.Context, identity.AuthRequest, string, int64, identity.RoleInput) (identity.Role, error)
	CopyRole(context.Context, identity.AuthRequest, string, int64, string) (identity.Role, error)
	DeleteRole(context.Context, identity.AuthRequest, string, int64) error
	EnableRole(context.Context, identity.AuthRequest, string, int64) (identity.Role, error)
	DisableRole(context.Context, identity.AuthRequest, string, int64) (identity.Role, error)
	ArchiveRole(context.Context, identity.AuthRequest, string, int64) (identity.Role, error)
	RestoreRole(context.Context, identity.AuthRequest, string, int64) (identity.Role, error)
}

var _ RoleService = (*identity.AuthService)(nil)

type roleChangesService interface {
	ChangeRole(context.Context, identity.AuthRequest, string, int64, identity.RoleChanges) (identity.Role, error)
}

type roleDisplayNameService interface {
	SetRoleDisplayName(context.Context, identity.AuthRequest, string, int64, string, *string) (identity.Role, error)
}

const (
	rolesPath = "/api/v1/admin/roles"
	rolePath  = rolesPath + "/{id}"
)

const (
	listRolesOperation       = "rolesList"
	getRoleOperation         = "rolesGet"
	createRoleOperation      = "rolesCreate"
	editRoleOperation        = "rolesEdit"
	changeRoleOperation      = "rolesApplyChanges"
	roleDisplayNameOperation = "rolesSetDisplayName"
	copyRoleOperation        = "rolesCopy"
	deleteRoleOperation      = "rolesDelete"
)

// A full 64-grant policy with explicit field UUID sets needs its own body cap.
// Credential endpoints retain their smaller independent limit.
const maxRoleBodyBytes = 256 * 1024

const (
	roleActive   = "active"
	roleDisabled = "disabled"
	roleArchived = "archived"
)

var roleRoutes = map[string][]route{
	rolesPath: {
		{kind: fullKind, method: http.MethodGet, operationID: listRolesOperation, summary: "List role policies", response: roleListSchema, description: "Requires a full session and current installation-wide roles.read under the shared security mutex. Returns explicit policy IDs, operations and scope flags in stable stored-name and UUID order; never content values, field labels, credentials or recipients. Read permission is independent of the mutation delegation envelope and does not require CSRF, fresh step-up or a denial budget. Default limit 20, maximum 32; offset at most 32."},
		{kind: fullKind, method: http.MethodPost, operationID: createRoleOperation, summary: "Create a disabled role", response: roleSchema, description: "Requires a full session, current roles.create, CSRF and strong authentication no older than five minutes. The entire explicit grant set must fit the actor's current delegation envelope. Creates only a disabled role without memberships; no roles.edit permission is required. Role, grants and audit commit atomically after the shared security mutex. Names confer no permissions."},
	},
	rolePath: {
		{kind: fullKind, method: http.MethodGet, operationID: getRoleOperation, summary: "Read a role policy", response: roleSchema, description: "Requires a full session and current installation-wide roles.read under the shared security mutex. Returns the explicit policy, including latent grants of disabled or archived roles, without content values, labels, credentials or recipients. This read is independent of the edit delegation envelope and does not require CSRF or fresh step-up."},
		{kind: fullKind, method: http.MethodPatch, operationID: editRoleOperation, summary: "Replace a role name and explicit grants", response: roleSchema, description: "Requires a full session, current roles.edit, CSRF and strong authentication no older than five minutes. Both name and grants are required; grants replace the entire explicit set atomically. Rechecks current permissions, target and positive expected_version under the shared security mutex. Old and new grants, latent grants and all affected recipients must fit the actor's envelope. Cannot increase the actor's own effective access or delegation, violate last-admin, change role state or memberships. Audit failure rolls back the complete change."},
		{kind: fullKind, method: http.MethodDelete, fields: expectedVersionField, operationID: deleteRoleOperation, summary: "Delete an archived role", description: "Requires a full session, roles.delete, CSRF and strong authentication no older than five minutes. The server checks expected_version under the shared security mutex. A non-archived role returns invalid_transition. Remaining memberships, grants or field dependencies on an archived role return role_in_use. Deletion and audit commit atomically. Prior audit records are preserved."},
	},
	rolePath + "/copy":         {{kind: fullKind, method: http.MethodPost, operationID: copyRoleOperation, summary: "Copy a role policy", response: roleSchema, description: "Requires a full session, both roles.create and roles.edit, CSRF and strong authentication no older than five minutes. The source version, explicit grants and delegation envelope are checked under the shared security mutex. Creates a disabled role with a new UUID and the supplied unique name. Memberships are never copied. A stale version or name collision returns version_conflict. Reaching the role limit returns invalid_transition. Creation, grants and audit commit atomically."}},
	rolePath + "/changes":      {{kind: fullKind, method: http.MethodPost, operationID: changeRoleOperation, summary: "Save changes to a role policy", response: roleSchema, description: "Requires a full session, current roles.edit, CSRF and strong authentication no older than five minutes. Applies the supplied name and grant changes to the current policy at the expected version. Grant identity is operation, collection UUID when applicable, and object scope when applicable. The entire resulting policy and affected recipients are checked under the security mutex. Changes and audit commit atomically."}},
	rolePath + "/display-name": {{kind: fullKind, method: http.MethodPost, operationID: roleDisplayNameOperation, summary: "Save a localized role label", response: roleSchema, description: "Requires roles.edit, CSRF, fresh strong authentication and the current role version. Updates or removes one label for an enabled language. The role and affected recipients must fit the actor's delegation envelope. Bound roles cannot be edited."}},
	rolePath + "/enable":       {roleStateRoute("rolesEnable", "roles.enable", roleDisabled, roleActive, RoleService.EnableRole)},
	rolePath + "/disable":      {roleStateRoute("rolesDisable", "roles.disable", roleActive, roleDisabled, RoleService.DisableRole)},
	rolePath + "/archive":      {roleStateRoute("rolesArchive", "roles.archive", roleDisabled, roleArchived, RoleService.ArchiveRole)},
	rolePath + "/restore":      {roleStateRoute("rolesRestore", "roles.restore", roleArchived, roleDisabled, RoleService.RestoreRole)},
}

type roleStateCommand func(RoleService, context.Context, identity.AuthRequest, string, int64) (identity.Role, error)

func roleStateRoute(operationID, permission, from, to string, command roleStateCommand) route {
	return route{kind: fullKind, method: http.MethodPost, fields: expectedVersionField, operationID: operationID, roleState: command, resultState: to, response: roleSchema,
		summary:     "Change a role from " + from + " to " + to,
		description: "Requires a full session, current " + permission + ", CSRF and strong authentication no older than five minutes. The role must be " + from + " and match expected_version. The server checks the role and all recipients against the actor's delegation envelope under the shared security mutex. The change preserves memberships and explicit grants. Only active roles supply effective grants. Invalid transitions and stale versions return 409."}
}

type roleGrantDTO struct {
	Operation          string   `json:"operation"`
	Scope              string   `json:"scope"`
	CanDelegate        bool     `json:"can_delegate"`
	TypeID             string   `json:"type_id,omitempty"`
	ObjectScope        string   `json:"object_scope,omitempty"`
	FieldIDs           []string `json:"field_ids,omitempty"`
	TargetScope        string   `json:"target_scope,omitempty"`
	ObjectCeiling      string   `json:"object_ceiling,omitempty"`
	MayDelegateContent *bool    `json:"may_delegate_content,omitempty"`
	DelegateOperations []string `json:"delegate_operations,omitempty"`
}

type roleWriteInput struct {
	Name            string         `json:"name" minLength:"1" maxLength:"128" doc:"Explicit role name without surrounding whitespace or control characters. Names confer no permissions."`
	Grants          []roleGrantDTO `json:"grants" doc:"Complete explicit policy; empty array removes all grants. Every referenced type and field must exist."`
	ExpectedVersion int64          `json:"expected_version,omitempty" minimum:"1" doc:"Positive current role version checked after authorization under the shared security mutex."`
}

type roleResponse struct {
	ID             string            `json:"id" format:"uuid" doc:"Stable role UUID."`
	Name           string            `json:"name" minLength:"1" maxLength:"128" doc:"Stored role name without authorization meaning."`
	State          string            `json:"state" enum:"disabled,active,archived" doc:"Current role lifecycle state; creation always returns disabled."`
	Version        int64             `json:"version" minimum:"1" doc:"Positive current role version."`
	Grants         []roleGrantDTO    `json:"grants"`
	CollectionKeys map[string]string `json:"collection_keys,omitempty" doc:"Registered collection keys indexed by the collection UUIDs that this role's grants reference. Omitted for roles without collection grants."`
	DisplayNames   map[string]string `json:"display_names,omitempty" doc:"Labels keyed by enabled language code. Fall back to name when absent."`
}

type roleListResponse struct {
	Items      []roleResponse `json:"items"`
	NextOffset *int           `json:"next_offset"`
}

func isRoleRoute(path string) bool { _, ok := roleRoutes[path]; return ok }

func roleRoutePath(path string) (string, string) {
	if path == rolesPath {
		return rolesPath, ""
	}
	if !strings.HasPrefix(path, rolesPath+"/") {
		return "", ""
	}
	id, action, hasAction := strings.Cut(strings.TrimPrefix(path, rolesPath+"/"), "/")
	if id == "" {
		return "", ""
	}
	if hasAction {
		key := rolePath + "/" + action
		if !isRoleRoute(key) {
			return "", ""
		}
		return key, id
	}
	return rolePath, id
}

func rolePage(query string) (identity.RolePage, error) {
	limit, offset, err := boundedPage(query, identity.DefaultRolePageLimit, identity.MaxRolePageLimit, identity.MaxRolePageOffset)
	return identity.RolePage{Limit: limit, Offset: offset}, err
}

func (h *handler) serveRole(w http.ResponseWriter, r *http.Request, rt route, id, ip string) {
	req, _, err := requestSession(r, fullKind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	if !attachRequestID(w, &req) {
		return
	}
	service, ok := h.service.(RoleService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if rt.operationID == listRolesOperation {
		h.listRoles(w, r, service, req)
		return
	}
	if id != "" && !roleUUID(id) {
		writeError(w, identity.ErrValidation)
		return
	}
	if rt.operationID == getRoleOperation {
		role, err := service.GetRole(r.Context(), req, id)
		respondRole(w, role, err)
		return
	}
	if rt.fields == expectedVersionField {
		serveRoleVersionCommand(w, r, rt, service, req, id)
		return
	}
	if rt.operationID == changeRoleOperation {
		serveRoleChanges(w, r, req, id, service)
		return
	}
	if rt.operationID == roleDisplayNameOperation {
		serveRoleDisplayName(w, r, req, id, service)
		return
	}
	input, code, status := readRoleInput(w, r, rt.operationID)
	if code != "" {
		problem(w, status, code)
		return
	}
	var role identity.Role
	switch rt.operationID {
	case createRoleOperation:
		role, err = service.CreateRole(r.Context(), req, rolePolicy(input))
	case copyRoleOperation:
		role, err = service.CopyRole(r.Context(), req, id, input.ExpectedVersion, input.Name)
	case editRoleOperation:
		role, err = service.EditRole(r.Context(), req, id, input.ExpectedVersion, rolePolicy(input))
	default:
		err = identity.ErrSecurityUnavailable
	}
	respondRole(w, role, err)
}

type roleDisplayNameInput struct {
	ExpectedVersion int64   `json:"expected_version"`
	LanguageCode    string  `json:"language_code"`
	DisplayName     *string `json:"display_name"`
}

func serveRoleDisplayName(w http.ResponseWriter, r *http.Request, req identity.AuthRequest, id string, service RoleService) {
	editor, ok := service.(roleDisplayNameService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	schema, _ := roleDisplayNameSchema()
	var input roleDisplayNameInput
	if !validateRoleContractWithNull(raw, schema, true, true) || json.Unmarshal(raw, &input) != nil {
		writeError(w, identity.ErrValidation)
		return
	}
	role, err := editor.SetRoleDisplayName(r.Context(), req, id, input.ExpectedVersion, input.LanguageCode, input.DisplayName)
	respondRole(w, role, err)
}

type roleChangesInput struct {
	ExpectedVersion int64                   `json:"expected_version"`
	Name            *string                 `json:"name,omitempty"`
	Upsert          []roleGrantDTO          `json:"upsert,omitempty"`
	Remove          []identity.RoleGrantKey `json:"remove,omitempty"`
}

func serveRoleChanges(w http.ResponseWriter, r *http.Request, req identity.AuthRequest, id string, service RoleService) {
	editor, ok := service.(roleChangesService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	raw, code, status := readJSONBody(w, r, maxRoleBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	schema, _ := roleRequestSchema(changeRoleOperation)
	var input roleChangesInput
	if !validateRoleContract(raw, schema, true) || json.Unmarshal(raw, &input) != nil {
		writeError(w, identity.ErrValidation)
		return
	}
	upsert := rolePolicy(roleWriteInput{Grants: input.Upsert}).Grants
	role, err := editor.ChangeRole(r.Context(), req, id, input.ExpectedVersion, identity.RoleChanges{Name: input.Name, Upsert: upsert, Remove: input.Remove})
	respondRole(w, role, err)
}

func serveRoleVersionCommand(w http.ResponseWriter, r *http.Request, rt route, service RoleService, req identity.AuthRequest, id string) {
	input, code, status := readRouteInput(w, r, rt)
	if code != "" {
		problem(w, status, code)
		return
	}
	if rt.operationID == deleteRoleOperation {
		if err := service.DeleteRole(r.Context(), req, id, input.ExpectedVersion); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	role, err := rt.roleState(service, r.Context(), req, id, input.ExpectedVersion)
	respondRole(w, role, err)
}

func respondRole(w http.ResponseWriter, role identity.Role, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, safeRole(role))
}

func (h *handler) listRoles(w http.ResponseWriter, r *http.Request, service RoleService, req identity.AuthRequest) {
	page, err := rolePage(r.URL.RawQuery)
	if err != nil {
		writeError(w, err)
		return
	}
	list, err := service.ListRoles(r.Context(), req, page)
	if err != nil {
		writeError(w, err)
		return
	}
	out := roleListResponse{Items: make([]roleResponse, len(list.Items)), NextOffset: list.NextOffset}
	for i, role := range list.Items {
		out.Items[i] = safeRole(role)
	}
	respond(w, out)
}

func rolePolicy(input roleWriteInput) identity.RoleInput {
	grants := make([]identity.RoleGrant, len(input.Grants))
	for i, grant := range input.Grants {
		grants[i] = identity.RoleGrant{Operation: grant.Operation, Scope: grant.Scope, CanDelegate: grant.CanDelegate, TypeID: grant.TypeID, ObjectScope: grant.ObjectScope, FieldIDs: grant.FieldIDs, TargetScope: grant.TargetScope, ObjectCeiling: grant.ObjectCeiling, MayDelegateContent: grant.MayDelegateContent != nil && *grant.MayDelegateContent, DelegateOperations: grant.DelegateOperations}
	}
	return identity.RoleInput{Name: input.Name, Grants: grants}
}

func safeRole(role identity.Role) roleResponse {
	grants := make([]roleGrantDTO, len(role.Grants))
	for i, grant := range role.Grants {
		grants[i] = roleGrantDTO{Operation: grant.Operation, Scope: grant.Scope, CanDelegate: grant.CanDelegate, TypeID: grant.TypeID, ObjectScope: grant.ObjectScope, FieldIDs: grant.FieldIDs, TargetScope: grant.TargetScope, ObjectCeiling: grant.ObjectCeiling, DelegateOperations: grant.DelegateOperations}
		if grant.Operation == roleAuthorityOperation {
			grants[i].MayDelegateContent = &grant.MayDelegateContent
		}
	}
	return roleResponse{ID: role.ID, Name: role.Name, State: role.State, Version: role.Version, Grants: grants, CollectionKeys: role.CollectionKeys(), DisplayNames: role.DisplayNames}
}

func readRoleInput(w http.ResponseWriter, r *http.Request, operationID string) (roleWriteInput, string, int) {
	var input roleWriteInput
	limit := int64(maxRoleBodyBytes)
	if operationID == copyRoleOperation {
		limit = maxCredentialBodyBytes
	}
	raw, code, status := readJSONBody(w, r, limit)
	if code != "" {
		return input, code, status
	}
	schema, _ := roleRequestSchema(operationID)
	if !validateRoleJSON(raw, schema) || json.Unmarshal(raw, &input) != nil {
		return input, validationCode, http.StatusBadRequest
	}
	return input, "", 0
}

// Schema validation shares the documented conditional grant shapes. A token
// walk rejects duplicate keys and null before Go's decoder can erase them.
func validateRoleJSON(raw []byte, schema *huma.Schema) bool {
	return validateRoleContract(raw, schema, false)
}

func validateRoleContract(raw []byte, schema *huma.Schema, exact bool) bool {
	return validateRoleContractWithNull(raw, schema, exact, false)
}

func validateRoleContractWithNull(raw []byte, schema *huma.Schema, exact, allowNull bool) bool {
	value, ok := decodeUniqueJSONWithNull(raw, allowNull)
	if !ok {
		return false
	}
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	registry.Map()[roleGrantSchema] = roleGrantContract()
	schema.PrecomputeMessages()
	registry.Map()[roleGrantSchema].PrecomputeMessages()
	if exact && (!utf8.Valid(raw) || !exactRoleContractKeys(registry, value, schema)) {
		return false
	}
	var result huma.ValidateResult
	huma.Validate(registry, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	return len(result.Errors) == 0
}

func exactRoleContractKeys(registry huma.Registry, value any, schema *huma.Schema) bool {
	if schema.Ref != "" {
		return exactRoleContractKeys(registry, value, registry.SchemaFromRef(schema.Ref))
	}
	if len(schema.OneOf) != 0 {
		for _, shape := range schema.OneOf {
			if exactRoleContractKeys(registry, value, shape) {
				return true
			}
		}
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		return exactRoleObjectKeys(registry, value, schema)
	case []any:
		for _, item := range value {
			if schema.Items == nil || !exactRoleContractKeys(registry, item, schema.Items) {
				return false
			}
		}
	}
	return true
}

func exactRoleObjectKeys(registry huma.Registry, value map[string]any, schema *huma.Schema) bool {
	for key, child := range value {
		property := schema.Properties[key]
		if property == nil || !exactRoleContractKeys(registry, child, property) {
			return false
		}
	}
	return true
}

func decodeUniqueJSON(raw []byte) (any, bool) {
	return decodeUniqueJSONWithNull(raw, false)
}

func decodeUniqueJSONWithNull(raw []byte, allowNull bool) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if !uniqueJSONValue(dec, allowNull) {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	var value any
	err := json.Unmarshal(raw, &value)
	return value, err == nil
}

func uniqueJSONValue(dec *json.Decoder, allowNull bool) bool {
	token, err := dec.Token()
	if err != nil || token == nil && !allowNull {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	if delim == '{' {
		return uniqueJSONObject(dec, allowNull)
	}
	if delim != '[' {
		return false
	}
	for dec.More() {
		if !uniqueJSONValue(dec, allowNull) {
			return false
		}
	}
	token, err = dec.Token()
	return err == nil && token == json.Delim(']')
}

func uniqueJSONObject(dec *json.Decoder, allowNull bool) bool {
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		if !uniqueJSONValue(dec, allowNull) {
			return false
		}
	}
	token, err := dec.Token()
	return err == nil && token == json.Delim('}')
}

func roleGrants(raw json.RawMessage) bool {
	return validateRoleJSON(raw, &huma.Schema{Type: huma.TypeArray, Items: schemaRef(roleGrantSchema), MaxItems: intPointer(identity.MaxRoleGrants)})
}

func roleUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return id != "00000000-0000-0000-0000-000000000000"
}
