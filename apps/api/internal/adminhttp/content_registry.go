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
	registryRenderersPath      = contentCollectionsPath + "/renderers"
	registryCollectionPath     = contentCollectionsPath + "/{type_key}"
	registryRenderersOperation = "registryListRenderers"
	registryReadOperation      = "registryReadCollection"
	registryCreateOperation    = "registryCreateCollection"
	registryReviseOperation    = "registryReviseCollection"
	registryCollectionSchema   = "ManagedCollection"
	registryRenderersSchema    = "CollectionRenderers"
	collectionExistsCode       = "collection_exists"
)

type RegistryEditor interface {
	ReadCollectionRenderers(context.Context, identity.AuthRequest) ([]content.CollectionRenderer, error)
	ReadCollectionForManagement(context.Context, identity.AuthRequest, string) (content.TypeSchema, error)
	CreateCollection(context.Context, identity.AuthRequest, string, []content.Definition, bool) (content.TypeSchema, error)
	ReviseCollection(context.Context, identity.AuthRequest, string, int64, []content.Definition, bool) (content.TypeSchema, error)
}

var registryRendererRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: registryRenderersOperation, response: registryRenderersSchema,
	summary: "List collection renderers", description: "Requires a full session and content_types.manage. Reports registered renderers and whether they already have a collection.",
}

var registryReadRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: registryReadOperation, response: registryCollectionSchema,
	summary: "Read a collection schema for management", description: "Requires a full session and content_types.manage. Returns stable field definitions and display labels from the current schema, without content values or item counts.",
}

var registryCreateRoute = route{
	kind: fullKind, method: http.MethodPost, operationID: registryCreateOperation, response: registryCollectionSchema,
	summary: "Create a collection", description: "Requires content_types.manage, a full session, CSRF and fresh MFA. The registered renderer determines the collection key. Initial access is explicit, limited to the newly created collection and fields, and guarded by additional permissions.",
}

var registryReviseRoute = route{
	kind: fullKind, method: http.MethodPatch, operationID: registryReviseOperation, response: registryCollectionSchema,
	summary: "Revise a collection schema", description: "Requires content_types.manage, a full session, CSRF, fresh MFA and the current collection version. Changes labels or adds optional fields. Explicit initial access applies only to new fields and existing operations and scopes within the actor's delegation authority.",
}

func registryTarget(path, method string) (routeTarget, route, bool) {
	switch path {
	case registryRenderersPath:
		return routeTarget{path: path}, registryRendererRoute, true
	case contentCollectionsPath:
		return routeTarget{path: path}, registryCreateRoute, method == http.MethodPost
	}
	key, ok := strings.CutPrefix(path, contentCollectionsPath+"/")
	if !ok || key == "" || strings.ContainsRune(key, '/') {
		return routeTarget{}, route{}, false
	}
	target := routeTarget{path: registryCollectionPath, typeKey: key}
	if method == http.MethodPatch {
		return target, registryReviseRoute, true
	}
	return target, registryReadRoute, true
}

func isRegistryOperation(operation string) bool {
	switch operation {
	case registryRenderersOperation, registryReadOperation, registryCreateOperation, registryReviseOperation:
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
	InitialAccess bool            `json:"initial_access,omitempty"`
	Fields        []registryField `json:"fields"`
}

type registryReviseInput struct {
	ExpectedVersion int64           `json:"expected_version,string"`
	Initialize      bool            `json:"initialize_new_field_access,omitempty"`
	Fields          []registryField `json:"fields"`
}

type registryCollectionResponse struct {
	TypeID    string          `json:"type_id"`
	TypeKey   string          `json:"type_key"`
	Namespace string          `json:"route_namespace"`
	Version   int64           `json:"version,string"`
	SchemaNo  int64           `json:"schema_no,string"`
	Fields    []registryField `json:"fields"`
}

type registryRenderersResponse struct {
	Items []registryRenderer `json:"items"`
}

type registryRenderer struct {
	TypeKey   string          `json:"type_key"`
	Namespace string          `json:"route_namespace"`
	Available bool            `json:"available"`
	Fields    []registryField `json:"fields"`
}

func collectionResponse(schema content.TypeSchema) registryCollectionResponse {
	return registryCollectionResponse{TypeID: schema.TypeID, TypeKey: schema.Key, Namespace: schema.RouteNamespace,
		Version: schema.Version, SchemaNo: schema.SchemaNo, Fields: registryFields(schema.Fields)}
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
	schema, err := runRegistryMutation(r.Context(), req, rt.operationID, target.typeKey, raw, service)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	if rt.operationID == registryCreateOperation {
		respondStatus(w, http.StatusCreated, collectionResponse(schema))
		return
	}
	respond(w, collectionResponse(schema))
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
		choices, err := service.ReadCollectionRenderers(r.Context(), req)
		if err != nil {
			writeRegistryError(w, err)
			return
		}
		out := registryRenderersResponse{Items: make([]registryRenderer, 0, len(choices))}
		for _, choice := range choices {
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

func runRegistryMutation(ctx context.Context, req identity.AuthRequest, operation, key string, raw []byte, service RegistryEditor) (content.TypeSchema, error) {
	value, ok := decodeUniqueJSON(raw)
	object, objectOK := value.(map[string]any)
	if !ok || !objectOK || !utf8.Valid(raw) || !validRegistryInput(object, operation) {
		return content.TypeSchema{}, identity.ErrValidation
	}
	if operation == registryCreateOperation {
		var input registryCreateInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return content.TypeSchema{}, identity.ErrValidation
		}
		return service.CreateCollection(ctx, req, input.Namespace, registryDefinitions(input.Fields), input.InitialAccess)
	}
	var input registryReviseInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return content.TypeSchema{}, identity.ErrValidation
	}
	return service.ReviseCollection(ctx, req, key, input.ExpectedVersion, registryDefinitions(input.Fields), input.Initialize)
}

func validRegistryInput(object map[string]any, operation string) bool {
	schema := registryWriteContracts[operation]
	if schema == nil || !onlyRegistryKeys(object, schema) {
		return false
	}
	if operation == registryReviseOperation {
		version, ok := object[expectedVersionField].(string)
		if !ok || !isContentVersion(version) {
			return false
		}
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
	if errors.As(err, &domain) && domain.Kind == content.Conflict && domain.Code == collectionExistsCode {
		problem(w, http.StatusConflict, domain.Code)
		return
	}
	writeDraftError(w, err)
}
