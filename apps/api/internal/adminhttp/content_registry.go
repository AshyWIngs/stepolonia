package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const (
	registryRenderersPath         = contentCollectionsPath + "/renderers"
	registryCollectionPath        = contentCollectionsPath + "/{type_key}"
	registryRenderersOperation    = "registryListRenderers"
	registryReadOperation         = "registryReadCollection"
	registryCreateOperation       = "registryCreateCollection"
	registryReviseOperation       = "registryReviseCollection"
	registryAvailabilityOperation = "registrySetCollectionAvailability"
	registryAvailabilityPath      = registryCollectionPath + "/availability"
	registryIconPath              = registryCollectionPath + "/icon"
	registryIconOperation         = "registrySaveCollectionIcon"
	registryIconSchema            = "CollectionIconChange"
	registryFieldChangesPath      = registryCollectionPath + "/fields"
	registryFieldChangesOperation = "registrySaveCollectionFields"
	registryCollectionSchema      = "ManagedCollection"
	registryRenderersSchema       = "CollectionRenderers"
	collectionExistsCode          = "collection_exists"
)

type RegistryEditor interface {
	ReadCollectionRenderers(context.Context, identity.AuthRequest) (content.ManagedCollectionCatalog, error)
	ReadCollectionForManagement(context.Context, identity.AuthRequest, string) (content.TypeSchema, error)
	CreateCollection(context.Context, identity.AuthRequest, content.CollectionCreate) (content.RegistryWriteResult, error)
	ReviseCollection(context.Context, identity.AuthRequest, content.CollectionRevision) (content.RegistryWriteResult, error)
	UpdateCollectionIcon(context.Context, identity.AuthRequest, string, string, string) (content.CollectionIconChange, error)
	ChangeCollectionFields(context.Context, identity.AuthRequest, content.CollectionFieldChanges) (content.RegistryWriteResult, error)
	SetCollectionAvailability(context.Context, identity.AuthRequest, string, int64, bool) (content.TypeSchema, error)
}

var registryRendererRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: registryRenderersOperation, response: registryRenderersSchema,
	summary: "List managed collections and templates", description: "Requires a full session and content_types.manage. Reports persisted collection identities, availability and templates for creation.",
}

var registryReadRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: registryReadOperation, response: registryCollectionSchema,
	summary: "Read a collection schema for management", description: "Requires a full session and content_types.manage. Returns stable field definitions and display labels from the current schema, without content values or item counts.",
}

var registryCreateRoute = route{
	kind: fullKind, method: http.MethodPost, operationID: registryCreateOperation, response: registryCollectionSchema,
	summary: "Create a collection", description: "Requires content_types.manage, a full session and CSRF. A built-in renderer determines the collection key. A generic collection requires a type key, a matching collection namespace and a display name. Initial access is explicit, limited to the new collection and fields, and guarded by fresh MFA and additional permissions.",
}

var registryReviseRoute = route{
	kind: fullKind, method: http.MethodPatch, operationID: registryReviseOperation, response: registryCollectionSchema,
	summary: "Revise a collection schema", description: "Requires content_types.manage, a full session, CSRF and the current collection version. Changes the navigation icon or labels, or adds optional fields. Explicit initial access requires fresh MFA and applies only to new fields and existing operations and scopes within the actor's delegation authority.",
}

var registryAvailabilityRoute = route{
	kind: fullKind, method: http.MethodPatch, operationID: registryAvailabilityOperation, response: registryCollectionSchema,
	summary: "Set collection availability for authoring", description: "Requires content_types.manage, a full session, CSRF, fresh MFA and the current collection version. Disabled collections are excluded from the authoring API. Stored content and published routes remain available according to their existing publication state.",
}

var registryIconRoute = route{
	kind: fullKind, method: http.MethodPost, operationID: registryIconOperation, response: registryIconSchema,
	summary: "Save a collection navigation icon", description: "Changes the icon using its current scoped version. Requires content_types.manage, a full session and CSRF.",
}

var registryFieldChangesRoute = route{
	kind: fullKind, method: http.MethodPost, operationID: registryFieldChangesOperation, response: registryCollectionSchema,
	summary: "Save changes to collection fields", description: "Adds optional fields against the current schema number or edits explicit display labels against the affected field versions. Requires a full session, CSRF and content_types.manage. Initial field access is explicit and requires fresh MFA and delegation authority.",
}

func registryTarget(path, method string) (routeTarget, route, bool) {
	switch path {
	case registryRenderersPath:
		return routeTarget{path: path}, registryRendererRoute, true
	case contentCollectionsPath:
		return routeTarget{path: path}, registryCreateRoute, method == http.MethodPost
	}
	key, ok := strings.CutPrefix(path, contentCollectionsPath+"/")
	if !ok {
		return routeTarget{}, route{}, false
	}
	if key == "" {
		return routeTarget{}, route{}, false
	}
	if base, action, found := strings.Cut(key, "/"); found {
		return registryChildTarget(base, action)
	}
	target := routeTarget{path: registryCollectionPath, typeKey: key}
	if method == http.MethodPatch {
		return target, registryReviseRoute, true
	}
	return target, registryReadRoute, true
}

func registryChildTarget(key, action string) (routeTarget, route, bool) {
	if key != "" {
		switch action {
		case "availability":
			return routeTarget{path: registryAvailabilityPath, typeKey: key}, registryAvailabilityRoute, true
		case "icon":
			return routeTarget{path: registryIconPath, typeKey: key}, registryIconRoute, true
		case "fields":
			return routeTarget{path: registryFieldChangesPath, typeKey: key}, registryFieldChangesRoute, true
		}
	}
	return routeTarget{}, route{}, false
}

func isRegistryOperation(operation string) bool {
	switch operation {
	case registryRenderersOperation, registryReadOperation, registryCreateOperation, registryReviseOperation, registryAvailabilityOperation, registryIconOperation, registryFieldChangesOperation:
		return true
	default:
		return false
	}
}

type registryField struct {
	Key           string          `json:"key"`
	Kind          string          `json:"kind"`
	Scope         string          `json:"scope"`
	Visibility    string          `json:"visibility"`
	Required      bool            `json:"required"`
	TargetTypeID  string          `json:"target_type_id,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
	DisplayLabels json.RawMessage `json:"display_labels,omitempty"`
}

type registryCreateInput struct {
	Namespace     string          `json:"route_namespace"`
	TypeKey       string          `json:"type_key,omitempty"`
	DisplayName   string          `json:"display_name,omitempty"`
	IconSVG       string          `json:"icon_svg,omitempty"`
	InitialAccess bool            `json:"initial_access,omitempty"`
	Fields        []registryField `json:"fields"`
}

type registryAvailabilityInput struct {
	ExpectedVersion int64 `json:"expected_version,string"`
	Enabled         bool  `json:"enabled"`
}

type registryReviseInput struct {
	ExpectedVersion int64           `json:"expected_version,string"`
	Initialize      bool            `json:"initialize_new_field_access,omitempty"`
	IconSVG         *string         `json:"icon_svg,omitempty"`
	Fields          []registryField `json:"fields"`
}

type managedRegistryField struct {
	FieldID      string `json:"field_id"`
	LabelVersion string `json:"label_version"`
	registryField
}

type registryAccessGrant struct {
	Operation string   `json:"operation"`
	Scope     string   `json:"object_scope,omitempty"`
	FieldIDs  []string `json:"field_ids,omitempty"`
}

type registryAccessReceipt struct {
	RoleID string                `json:"role_id" format:"uuid"`
	Grants []registryAccessGrant `json:"grants"`
}

type registryCollectionResponse struct {
	TypeID      string                 `json:"type_id"`
	TypeKey     string                 `json:"type_key"`
	Namespace   string                 `json:"route_namespace"`
	DisplayName string                 `json:"display_name"`
	IconSVG     string                 `json:"icon_svg"`
	IconVersion string                 `json:"icon_version"`
	Enabled     bool                   `json:"enabled"`
	Version     int64                  `json:"version,string"`
	SchemaNo    int64                  `json:"schema_no,string"`
	Fields      []managedRegistryField `json:"fields"`
	Access      *registryAccessReceipt `json:"applied_access,omitempty"`
}

type registryRenderersResponse struct {
	Items       []registryRenderer          `json:"items"`
	Collections []content.CollectionSummary `json:"collections"`
}

type registryRenderer struct {
	TypeKey   string          `json:"type_key"`
	Namespace string          `json:"route_namespace"`
	Available bool            `json:"available"`
	Fields    []registryField `json:"fields"`
}

func collectionResponse(schema content.TypeSchema) registryCollectionResponse {
	definitions := registryFields(schema.Fields)
	fields := make([]managedRegistryField, 0, len(definitions))
	for index, field := range definitions {
		definition := schema.Fields[index]
		fields = append(fields, managedRegistryField{FieldID: definition.ID, LabelVersion: content.FieldLabelVersion(definition.DisplayLabels), registryField: field})
	}
	return registryCollectionResponse{TypeID: schema.TypeID, TypeKey: schema.Key, Namespace: schema.RouteNamespace,
		DisplayName: schema.DisplayName, IconSVG: schema.IconSVG, IconVersion: content.CollectionIconVersion(schema.IconSVG),
		Enabled: schema.Enabled, Version: schema.Version, SchemaNo: schema.SchemaNo, Fields: fields}
}

func registryFields(fields []content.Definition) []registryField {
	out := make([]registryField, 0, len(fields))
	for _, field := range fields {
		out = append(out, registryField{
			Key: field.Key, Kind: field.Kind, Scope: field.Scope, Visibility: field.Visibility, Required: field.Required,
			TargetTypeID: field.TargetTypeID, Config: field.Config, DisplayLabels: field.DisplayLabels,
		})
	}
	return out
}

func registryDefinitions(fields []registryField) []content.Definition {
	out := make([]content.Definition, 0, len(fields))
	for _, field := range fields {
		out = append(out, content.Definition{
			Key: field.Key, Kind: field.Kind, Scope: field.Scope, Visibility: field.Visibility, Required: field.Required,
			TargetTypeID: field.TargetTypeID, Config: field.Config, DisplayLabels: field.DisplayLabels,
		})
	}
	return out
}

func (h *handler) serveRegistry(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	service, ok := h.draftEditor.(RegistryEditor)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if r.Method == http.MethodGet {
		h.serveRegistryRead(w, r, target, ip, service)
		return
	}
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	raw, code, status := readJSONBody(w, r, maxDraftBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	if rt.operationID == registryIconOperation {
		serveRegistryIcon(w, r.Context(), req, target.typeKey, raw, service)
		return
	}
	result, err := runRegistryMutation(r.Context(), req, rt.operationID, target.typeKey, raw, service)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	out := collectionResponse(result.Schema)
	if result.Access.RoleID != "" {
		out.Access = registryReceipt(result.Access)
	}
	if rt.operationID == registryCreateOperation {
		respondStatus(w, http.StatusCreated, out)
		return
	}
	respond(w, out)
}

type registryIconInput struct {
	ExpectedVersion string `json:"expected_version"`
	IconSVG         string `json:"icon_svg"`
}

type registryIconResponse struct {
	PreviousVersion int64  `json:"previous_version,string"`
	Version         int64  `json:"version,string"`
	IconSVG         string `json:"icon_svg"`
	IconVersion     string `json:"icon_version"`
}

func serveRegistryIcon(w http.ResponseWriter, ctx context.Context, req identity.AuthRequest, key string, raw []byte, service RegistryEditor) {
	var input registryIconInput
	if !decodeClosedCommand(raw, registryWriteContracts[registryIconOperation], &input) {
		writeError(w, identity.ErrValidation)
		return
	}
	changed, err := service.UpdateCollectionIcon(ctx, req, key, input.ExpectedVersion, input.IconSVG)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	respond(w, registryIconResponse{changed.PreviousVersion, changed.Version, changed.IconSVG, content.CollectionIconVersion(changed.IconSVG)})
}

func registryReceipt(access identity.CollectionAccess) *registryAccessReceipt {
	grants := make([]registryAccessGrant, 0, len(access.Grants))
	for _, grant := range access.Grants {
		grants = append(grants, registryAccessGrant{Operation: grant.Operation, Scope: grant.ObjectScope, FieldIDs: grant.FieldIDs})
	}
	return &registryAccessReceipt{RoleID: access.RoleID, Grants: grants}
}

func (h *handler) serveRegistryRead(w http.ResponseWriter, r *http.Request, target routeTarget, ip string, service RegistryEditor) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, identity.ErrValidation)
		return
	}
	req, _, err := requestSession(r, fullKind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	if target.path == registryRenderersPath {
		catalog, err := service.ReadCollectionRenderers(r.Context(), req)
		if err != nil {
			writeRegistryError(w, err)
			return
		}
		out := registryRenderersResponse{Items: make([]registryRenderer, 0, len(catalog.Renderers)), Collections: catalog.Collections}
		for _, choice := range catalog.Renderers {
			out.Items = append(out.Items, registryRenderer{
				TypeKey: choice.Key, Namespace: choice.Namespace, Available: choice.Available, Fields: registryFields(choice.Fields),
			})
		}
		respond(w, out)
		return
	}
	schema, err := service.ReadCollectionForManagement(r.Context(), req, target.typeKey)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	respond(w, collectionResponse(schema))
}

func runRegistryMutation(ctx context.Context, req identity.AuthRequest, operation, key string, raw []byte, service RegistryEditor) (content.RegistryWriteResult, error) {
	if operation == registryFieldChangesOperation {
		return runRegistryFieldChanges(ctx, req, key, raw, service)
	}
	value, ok := decodeUniqueJSON(raw)
	object, objectOK := value.(map[string]any)
	if !ok || !objectOK || !utf8.Valid(raw) || !validRegistryInput(object, operation) {
		return content.RegistryWriteResult{}, identity.ErrValidation
	}
	if operation == registryCreateOperation {
		var input registryCreateInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return content.RegistryWriteResult{}, identity.ErrValidation
		}
		return service.CreateCollection(ctx, req, content.CollectionCreate{Key: input.TypeKey, Namespace: input.Namespace,
			DisplayName: input.DisplayName, IconSVG: input.IconSVG, Fields: registryDefinitions(input.Fields), InitialAccess: input.InitialAccess})
	}
	if operation == registryAvailabilityOperation {
		var input registryAvailabilityInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return content.RegistryWriteResult{}, identity.ErrValidation
		}
		updated, err := service.SetCollectionAvailability(ctx, req, key, input.ExpectedVersion, input.Enabled)
		return content.RegistryWriteResult{Schema: updated}, err
	}
	var input registryReviseInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return content.RegistryWriteResult{}, identity.ErrValidation
	}
	return service.ReviseCollection(ctx, req, content.CollectionRevision{TypeKey: key, ExpectedVersion: input.ExpectedVersion,
		Fields: registryDefinitions(input.Fields), Initialize: input.Initialize, IconSVG: input.IconSVG})
}

func validRegistryInput(object map[string]any, operation string) bool {
	schema := registryWriteContracts[operation]
	if schema == nil || !onlyRegistryKeys(object, schema) {
		return false
	}
	if operation != registryCreateOperation {
		version, ok := object[expectedVersionField].(string)
		if !ok || !isContentVersion(version) {
			return false
		}
	}
	if operation == registryAvailabilityOperation {
		_, ok := object[registryEnabledField].(bool)
		return ok
	}
	fields, ok := object[contentFieldsProperty].([]any)
	if !ok || len(fields) == 0 || len(fields) > 64 {
		return false
	}
	itemSchema := schema.Properties[contentFieldsProperty].Items
	for _, value := range fields {
		field, ok := value.(map[string]any)
		if !ok || !onlyRegistryKeys(field, itemSchema) {
			return false
		}
	}
	return true
}

func onlyRegistryKeys(object map[string]any, schema *huma.Schema) bool {
	for key, value := range object {
		if schema.Properties[key] == nil || value == nil {
			return false
		}
	}
	for _, required := range schema.Required {
		if _, ok := object[required]; !ok {
			return false
		}
	}
	return true
}

func writeRegistryError(w http.ResponseWriter, err error) {
	var domain *content.Error
	if errors.As(err, &domain) {
		switch domain.Code {
		case collectionExistsCode:
			problem(w, http.StatusConflict, domain.Code)
			return
		case content.InvalidCollectionIconCode:
			problem(w, http.StatusBadRequest, domain.Code)
			return
		}
	}
	writeDraftError(w, err)
}
