package adminhttp

import (
	"context"
	"net/http"

	"stepolonia/apps/api/internal/identity"
)

const (
	readUserRolesOperation        = "usersReadRoleMemberships"
	readContentAssigneesOperation = "contentReadAssignees"
	roleMembershipSetSchema       = "RoleMembershipSet"
	contentAssignmentSetSchema    = "ContentAssignmentSet"
)

var readUserRolesRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: readUserRolesOperation, response: roleMembershipSetSchema,
	summary:     "Read an account's role membership set",
	description: "Requires a full session and current roles.assign permission. Returns the account version, complete membership set and eligible role choices grouped by permission domain. Targets outside the actor's delegation envelope are hidden. Replacement requires fresh strong authentication and revalidates the whole set.",
}

var readContentAssigneesRoute = route{
	kind: fullKind, method: http.MethodGet, operationID: readContentAssigneesOperation, response: contentAssignmentSetSchema,
	summary:     "Read a content item's assignment set",
	description: "Requires a full session and content.items.assign with any scope on the item's actual type. Returns the current item version, complete assignment set and eligible named account choices. An item with an out-of-envelope assignee is hidden. Existing inactive assignees can be removed. Replacement revalidates the whole set and requires fresh strong authentication.",
}

type accessReadSetsService interface {
	ReadUserRoleMemberships(context.Context, identity.AuthRequest, string) (identity.RoleMembershipSet, error)
	ReadContentAssignees(context.Context, identity.AuthRequest, string, string) (identity.ContentAssignmentSet, error)
}

var _ accessReadSetsService = (*identity.AuthService)(nil)

type roleMembershipOption struct {
	ID     string `json:"id" format:"uuid"`
	Name   string `json:"name"`
	State  string `json:"state" enum:"active,disabled,archived"`
	Domain string `json:"domain" enum:"content,structure,settings,access,mixed,other" doc:"Display category based on explicit role grants. Mixed roles span several domains. Other roles have no grants."`
}

type roleMembershipSetResponse struct {
	UserID   string                 `json:"user_id" format:"uuid"`
	Version  int64                  `json:"version" minimum:"1"`
	RoleIDs  []string               `json:"role_ids"`
	Options  []roleMembershipOption `json:"options"`
	MaxRoles int                    `json:"max_roles" minimum:"1"`
}

type contentAssigneeOption struct {
	ID    string `json:"id" format:"uuid"`
	Login string `json:"login"`
	State string `json:"state" enum:"active,disabled,pending"`
}

type contentAssignmentSetResponse struct {
	contentAssigneesResponse
	Options      []contentAssigneeOption `json:"options"`
	MaxAssignees int                     `json:"max_assignees" minimum:"1"`
}

// unpagedReadRequest restricts private reads to a session and a bodyless URL.
func unpagedReadRequest(w http.ResponseWriter, r *http.Request, kind, ip string) (identity.AuthRequest, bool) {
	req, _, err := requestSession(r, kind, ip)
	if err != nil {
		writeError(w, err)
		return identity.AuthRequest{}, false
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, identity.ErrValidation)
		return identity.AuthRequest{}, false
	}
	return req, true
}

func (h *handler) serveAccessReadSet(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	req, ok := unpagedReadRequest(w, r, rt.kind, ip)
	if !ok {
		return
	}
	service, ok := h.service.(accessReadSetsService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if rt.operationID == readUserRolesOperation {
		set, err := service.ReadUserRoleMemberships(r.Context(), req, target.id)
		if err != nil {
			writeError(w, err)
			return
		}
		options := make([]roleMembershipOption, 0, len(set.Options))
		for _, option := range set.Options {
			options = append(options, roleMembershipOption(option))
		}
		respond(w, roleMembershipSetResponse{UserID: set.UserID, Version: set.Version, RoleIDs: set.RoleIDs, Options: options, MaxRoles: identity.MaxUserRoles})
		return
	}
	set, err := service.ReadContentAssignees(r.Context(), req, target.typeKey, target.id)
	if err != nil {
		writeError(w, err)
		return
	}
	options := make([]contentAssigneeOption, 0, len(set.Options))
	for _, option := range set.Options {
		options = append(options, contentAssigneeOption(option))
	}
	respond(w, contentAssignmentSetResponse{
		contentAssigneesResponse: contentAssigneesResponse{ItemID: set.ItemID, Version: set.Version, AssigneeIDs: set.AssigneeIDs},
		Options:                  options,
		MaxAssignees:             identity.MaxContentAssignees,
	})
}
