package cmshttp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"stepolonia/apps/api/internal/cms"
)

// LifecycleService supplies authorized safe reads and versioned account commands.
type LifecycleService interface {
	ListUsers(context.Context, cms.AuthRequest, cms.UserPage) (cms.UserList, error)
	GetUser(context.Context, cms.AuthRequest, string) (cms.User, error)
	ActivateUser(context.Context, cms.AuthRequest, string, int64) (cms.User, error)
	DisableUser(context.Context, cms.AuthRequest, string, int64) (cms.User, error)
}

var _ LifecycleService = (*cms.AuthService)(nil)

const (
	userPath         = usersPath + "/{id}"
	activateUserPath = userPath + "/activate"
	disableUserPath  = userPath + "/disable"
)

const (
	listUsersOperation    = "usersList"
	getUserOperation      = "usersGet"
	activateUserOperation = "usersActivate"
	disableUserOperation  = "usersDisable"
)

var listUsersRoute = route{kind: fullKind, method: http.MethodGet, operationID: listUsersOperation, summary: "List authorized accounts", response: userListSchema,
	description: "Requires a full session and current users.read permission, rechecked under the shared security mutex. Returns only safe account fields after authorization and before bounded pagination; no credential, factor, token or grant details. Does not require CSRF or fresh strong authentication. Default limit is 20, maximum 100; offset is bounded by 100. Results have a stable normalized-login and UUID order and next_offset is null when no further authorized account remains."}
var getUserRoute = route{kind: fullKind, method: http.MethodGet, operationID: getUserOperation, summary: "Read an authorized account", response: safeUserSchema,
	description: "Requires a full session and current users.read permission under the shared security mutex. Returns only UUID, normalized login, account state and version. A missing target returns not_found; absent users.read permission returns permission_denied. Does not require CSRF or fresh strong authentication."}
var activateUserRoute = route{kind: fullKind, fields: expectedVersionField, method: http.MethodPost, operationID: activateUserOperation, summary: "Activate an enrolled account", response: safeUserSchema,
	description: "Requires a full session, current users.activate permission, CSRF and strong authentication no older than five minutes. A disabled account with completed password, confirmed TOTP and unused recovery codes becomes active only when expected_version matches under the shared mutex. Creates no roles or memberships. Existing latent grants must fit the actor's delegation envelope and cannot increase the actor's own access. State change and audit commit atomically."}
var disableUserRoute = route{kind: fullKind, fields: expectedVersionField, method: http.MethodPost, operationID: disableUserOperation, summary: "Disable an active account", response: safeUserSchema,
	description: "Requires a full session, current users.disable permission, CSRF and strong authentication no older than five minutes. The active target and expected_version are rechecked under the shared mutex; affected access must fit the actor's delegation envelope. Atomically disables the account and revokes its full and restricted sessions, challenges, recovery and enrollment attempts. Preserves at least one effective recoverable administrator. State change and audit commit atomically."}

type safeUserResponse struct {
	ID      string `json:"id" format:"uuid" doc:"Stable account UUID."`
	Login   string `json:"login" doc:"Normalized account login identifier."`
	State   string `json:"state" enum:"pending,disabled,active" doc:"Current account lifecycle state."`
	Version int64  `json:"version" minimum:"1" doc:"Current account version, required for versioned account commands."`
}

type userListResponse struct {
	Items      []safeUserResponse `json:"items" doc:"Authorized safe account projections in stable normalized-login and UUID order."`
	NextOffset *int               `json:"next_offset" minimum:"0" doc:"Offset for the next page; null when no authorized accounts remain."`
}

func safeUser(user cms.User) safeUserResponse {
	return safeUserResponse{ID: user.ID, Login: user.Login, State: user.State, Version: user.Version}
}

func isLifecycleRoute(path string) bool {
	return path == usersPath || path == userPath || path == activateUserPath || path == disableUserPath
}

func userPage(query string) (cms.UserPage, error) {
	limit, offset, err := boundedPage(query, cms.DefaultUserPageLimit, cms.MaxUserPageLimit, cms.MaxUserPageOffset)
	return cms.UserPage{Limit: limit, Offset: offset}, err
}

func boundedPage(query string, defaultLimit, maxLimit, maxOffset int) (int, int, error) {
	limit, offset := defaultLimit, 0
	values, err := url.ParseQuery(query)
	if err != nil {
		return 0, 0, cms.ErrValidation
	}
	for key, values := range values {
		if len(values) != 1 {
			return 0, 0, cms.ErrValidation
		}
		value, err := unsignedDecimal(values[0])
		if err != nil {
			return 0, 0, err
		}
		switch key {
		case "limit":
			limit = value
		case "offset":
			offset = value
		default:
			return 0, 0, cms.ErrValidation
		}
	}
	if limit < 1 || limit > maxLimit || offset > maxOffset {
		return 0, 0, cms.ErrValidation
	}
	return limit, offset, nil
}

func unsignedDecimal(raw string) (int, error) {
	if raw == "" {
		return 0, cms.ErrValidation
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, cms.ErrValidation
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, cms.ErrValidation
	}
	return value, nil
}

func (h *handler) executeLifecycle(w http.ResponseWriter, r *http.Request, operation, id string, req cms.AuthRequest, input credentials) {
	service, ok := h.service.(LifecycleService)
	if !ok {
		writeError(w, cms.ErrSecurityUnavailable)
		return
	}
	if operation == listUsersOperation {
		h.listUsers(w, r, service, req)
		return
	}
	var user cms.User
	var err error
	switch operation {
	case getUserOperation:
		user, err = service.GetUser(r.Context(), req, id)
	case activateUserOperation:
		user, err = service.ActivateUser(r.Context(), req, id, input.ExpectedVersion)
	case disableUserOperation:
		user, err = service.DisableUser(r.Context(), req, id, input.ExpectedVersion)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, safeUser(user))
}

func (h *handler) listUsers(w http.ResponseWriter, r *http.Request, service LifecycleService, req cms.AuthRequest) {
	page, err := userPage(r.URL.RawQuery)
	if err != nil {
		writeError(w, err)
		return
	}
	users, err := service.ListUsers(r.Context(), req, page)
	if err != nil {
		writeError(w, err)
		return
	}
	out := userListResponse{Items: make([]safeUserResponse, len(users.Items)), NextOffset: users.NextOffset}
	for i, user := range users.Items {
		out.Items[i] = safeUser(user)
	}
	respond(w, out)
}
