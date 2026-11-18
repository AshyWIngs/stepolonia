package adminhttp

import (
	"context"
	"net/http"

	"stepolonia/apps/api/internal/identity"
)

const (
	roleGrantOptionsPath          = rolesPath + "/grant-options"
	readRoleGrantOptionsOperation = "rolesReadGrantOptions"
	roleGrantOptionsSchema        = "RoleGrantOptions"
)

var readRoleGrantOptionsRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: readRoleGrantOptionsOperation, response: roleGrantOptionsSchema,
	summary:     "Read grant options within the delegation envelope",
	description: "Requires a full session and current roles.create or roles.edit. Each option represents one covering source and may be narrowed independently; options do not authorize combining grants. Returns only operation keys and eligible collection and field metadata. Draft data, assignments, recipients and credentials are excluded.",
}

type roleGrantOptionsReader interface {
	ReadRoleGrantOptions(context.Context, identity.AuthRequest) ([]identity.RoleGrantOption, error)
}

var _ roleGrantOptionsReader = (*identity.AuthService)(nil)

type roleGrantOptionField struct {
	ID  string `json:"id" format:"uuid"`
	Key string `json:"key"`
}

type roleGrantOptionDTO struct {
	roleGrantDTO
	TypeKey string                 `json:"type_key,omitempty"`
	Fields  []roleGrantOptionField `json:"fields,omitempty"`
}

type roleGrantOptionsResponse struct {
	Options []roleGrantOptionDTO `json:"options"`
}

func (h *handler) serveRoleGrantOptions(w http.ResponseWriter, r *http.Request, rt route, ip string) {
	req, ok := unpagedReadRequest(w, r, rt.kind, ip)
	if !ok {
		return
	}
	service, ok := h.service.(roleGrantOptionsReader)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	values, err := service.ReadRoleGrantOptions(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	options := make([]roleGrantOptionDTO, 0, len(values))
	for _, value := range values {
		options = append(options, safeGrantOption(value))
	}
	respond(w, roleGrantOptionsResponse{Options: options})
}

func safeGrantOption(option identity.RoleGrantOption) roleGrantOptionDTO {
	view := roleGrantOptionDTO{
		roleGrantDTO: roleGrantDTO{
			Operation: option.Operation, Scope: option.Scope, CanDelegate: option.CanDelegate,
			TypeID: option.TypeID, ObjectScope: option.ObjectScope, TargetScope: option.TargetScope,
			ObjectCeiling: option.ObjectCeiling, DelegateOperations: option.DelegateOperations,
		},
		TypeKey: option.TypeKey,
	}
	if option.TargetScope != "" {
		view.MayDelegateContent = &option.MayDelegateContent
	}
	for _, field := range option.Fields {
		view.Fields = append(view.Fields, roleGrantOptionField(field))
	}
	return view
}
