package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"stepolonia/apps/api/internal/identity"
)

const (
	accountPath           = "/api/v1/admin/me"
	accountPasswordPath   = accountPath + "/password"
	accountFactorPath     = accountPath + "/mfa/replace"
	accountCodesPath      = accountPath + "/recovery-codes"
	accountSessionsPath   = "/api/v1/admin/sessions"
	targetSessionsPath    = userPath + "/sessions"
	revokeSessionPath     = accountSessionsPath + "/{id}/revoke"
	revokeAllSessionsPath = accountSessionsPath + "/revoke-all"
	auditPath             = "/api/v1/admin/audit"

	accountProfileOperation     = "accountReadProfile"
	accountPasswordOperation    = "accountChangePassword"
	accountFactorOperation      = "accountReplaceFactor"
	accountCodesOperation       = "accountRegenerateRecoveryCodes"
	listSessionsOperation       = "accountListSessions"
	listTargetSessionsOperation = "accountListTargetSessions"
	revokeSessionOperation      = "accountRevokeSession"
	revokeAllSessionsOperation  = "accountRevokeAllSessions"
	listAuditOperation          = "listSecurityAudit"
	accountProfileSchema        = "AccountProfile"
	accountSessionsSchema       = "AccountSessions"
	auditListSchema             = "SecurityAuditPage"
	factorPreparationSchema     = "FactorReplacementPreparation"
	replacementTokenField       = "replacement_token"
)

type accountService interface {
	ReadAccountProfile(context.Context, identity.AuthRequest) (identity.AccountProfile, error)
	ListAccountSessions(context.Context, identity.AuthRequest, string, identity.AccountPage) ([]identity.AccountSession, error)
	RevokeAccountSession(context.Context, identity.AuthRequest, string) (bool, error)
	RevokeAll(context.Context, identity.AuthRequest) error
	ListSecurityAudit(context.Context, identity.AuthRequest, identity.AccountPage) ([]identity.SecurityAuditEntry, error)
	ChangeAccountPassword(context.Context, identity.AuthRequest, string) error
	PrepareFactorReplacement(context.Context, identity.AuthRequest) (identity.FactorPreparation, error)
	CompleteFactorReplacement(context.Context, identity.AuthRequest, string, string) ([]string, error)
	RegenerateRecoveryCodes(context.Context, identity.AuthRequest) ([]string, error)
}

var _ accountService = (*identity.AuthService)(nil)

var accountRoutes = map[string]route{
	accountPath: {kind: fullKind, method: http.MethodGet, operationID: accountProfileOperation, response: accountProfileSchema,
		summary: "Read the signed-in account", description: "Reports the signed-in account, whether MFA is confirmed and the number of unused recovery codes."},
	accountPasswordPath: {kind: fullKind, method: http.MethodPost, operationID: accountPasswordOperation, fields: "password",
		summary: "Change the signed-in account password", description: "Requires fresh password and TOTP authentication. Revokes all account sessions and clears browser cookies after commit."},
	accountFactorPath: {kind: fullKind, method: http.MethodPost, operationID: accountFactorOperation, fields: replacementTokenField + "," + credentialCodeField, response: factorPreparationSchema,
		summary: "Prepare or confirm a replacement authenticator", description: "Requires fresh password and TOTP authentication. Send an empty object to prepare a new seed. Then send the replacement token and a fresh code from that seed using the same session. Confirmation replaces the factor, invalidates old recovery codes and revokes all sessions atomically."},
	accountCodesPath: {kind: fullKind, method: http.MethodPost, operationID: accountCodesOperation, response: recoveryCodesSchema,
		summary: "Regenerate the signed-in account's recovery codes", description: "Requires fresh password and TOTP authentication. Send an empty object. Invalidates all old codes and revokes all sessions. The response contains ten new codes once."},
	accountSessionsPath: {kind: fullKind, method: http.MethodGet, operationID: listSessionsOperation, response: accountSessionsSchema,
		summary: "List the signed-in account's active sessions", description: "Reports a bounded page of active full sessions in descending creation order. Identifiers cannot be used as authentication credentials."},
	targetSessionsPath: {kind: fullKind, method: http.MethodGet, operationID: listTargetSessionsOperation, response: accountSessionsSchema,
		summary: "List active sessions for a selected account", description: "Requires sessions.revoke_any, a target inside the actor's delegation envelope and fresh strong authentication for another account. Returns only full-session metadata."},
	revokeSessionPath: {kind: fullKind, method: http.MethodPost, operationID: revokeSessionOperation,
		summary: "Revoke a full session", description: "Send an empty object. Revoking another account's session additionally requires sessions.revoke_any, an eligible target and fresh MFA. Revoking the current session clears the browser cookies."},
	revokeAllSessionsPath: {kind: fullKind, method: http.MethodPost, operationID: revokeAllSessionsOperation,
		summary: "Revoke all sessions for the signed-in account", description: "Requires fresh password and TOTP authentication. Send an empty object. Revokes all sessions for the current account and clears browser cookies."},
	auditPath: {kind: fullKind, method: http.MethodGet, operationID: listAuditOperation, response: auditListSchema,
		summary: "List security audit events", description: "Requires audit.read. Returns a bounded page in descending event order with immutable identifiers and reason codes. Excludes secrets and arbitrary event details."},
}

func accountTarget(path string) (string, string) {
	if _, ok := accountRoutes[path]; ok && path != revokeSessionPath {
		return path, ""
	}
	id, ok := strings.CutPrefix(path, accountSessionsPath+"/")
	if !ok {
		return "", ""
	}
	id, ok = strings.CutSuffix(id, "/revoke")
	if ok && id != "" && !strings.ContainsRune(id, '/') {
		return revokeSessionPath, id
	}
	return "", ""
}

type accountProfileResponse struct {
	ID                     string `json:"id" format:"uuid"`
	Login                  string `json:"login"`
	MFAEnabled             bool   `json:"mfa_enabled"`
	RecoveryCodesRemaining int    `json:"recovery_codes_remaining"`
}

type accountSessionResponse struct {
	ID        string    `json:"id" format:"uuid"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

type auditEntryResponse struct {
	ID         string    `json:"id" format:"uuid"`
	OccurredAt time.Time `json:"occurred_at"`
	Operation  string    `json:"operation"`
	Result     string    `json:"result"`
	Reason     string    `json:"reason,omitempty"`
	ActorID    string    `json:"actor_id,omitempty" format:"uuid"`
	TargetID   string    `json:"target_id,omitempty" format:"uuid"`
	RequestID  string    `json:"request_id,omitempty" format:"uuid"`
}

type accountPageResponse[T any] struct {
	Items []T `json:"items"`
}

type factorPreparationResponse struct {
	Secret string `json:"secret"`
	Token  string `json:"replacement_token"`
}

func (h *handler) serveAccount(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	service, ok := h.service.(accountService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if rt.method == http.MethodGet {
		h.serveAccountRead(w, r, rt, target.id, ip, service)
		return
	}
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	if rt.operationID == accountFactorOperation {
		serveFactorReplacement(w, r, service, req, rt)
		return
	}
	input, code, status := readCredentials(w, r, rt.fields)
	if code != "" {
		problem(w, status, code)
		return
	}
	executeAccountWrite(w, r, service, req, rt.operationID, target.id, input.Password)
}

func (h *handler) serveAccountRead(w http.ResponseWriter, r *http.Request, rt route, id, ip string, service accountService) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, identity.ErrValidation)
		return
	}
	req, _, err := requestSession(r, fullKind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	switch rt.operationID {
	case accountProfileOperation:
		if r.URL.RawQuery != "" {
			writeError(w, identity.ErrValidation)
			return
		}
		profile, err := service.ReadAccountProfile(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, accountProfileResponse{ID: profile.ID, Login: profile.Login, MFAEnabled: profile.MFAEnabled, RecoveryCodesRemaining: profile.RecoveryCodesRemaining})
	case listSessionsOperation, listTargetSessionsOperation, listAuditOperation:
		listAccountResource(w, r, req, service, rt.operationID, id)
	}
}

func listAccountResource(w http.ResponseWriter, r *http.Request, req identity.AuthRequest, service accountService, operation, id string) {
	limit, offset, err := boundedPage(r.URL.RawQuery, identity.DefaultAccountPageLimit, identity.MaxAccountPageLimit, identity.MaxAccountPageOffset)
	if err != nil {
		writeError(w, err)
		return
	}
	page := identity.AccountPage{Limit: limit, Offset: offset}
	if operation != listAuditOperation {
		items, err := service.ListAccountSessions(r.Context(), req, id, page)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]accountSessionResponse, 0, len(items))
		for _, item := range items {
			out = append(out, accountSessionResponse(item))
		}
		respond(w, accountPageResponse[accountSessionResponse]{Items: out})
		return
	}
	items, err := service.ListSecurityAudit(r.Context(), req, page)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]auditEntryResponse, 0, len(items))
	for _, item := range items {
		out = append(out, auditEntryResponse{ID: item.ID, OccurredAt: item.OccurredAt, Operation: item.Operation, Result: item.Result,
			Reason: item.Reason, ActorID: item.ActorID, TargetID: item.TargetID, RequestID: item.RequestID})
	}
	respond(w, accountPageResponse[auditEntryResponse]{Items: out})
}

func executeAccountWrite(w http.ResponseWriter, r *http.Request, service accountService, req identity.AuthRequest, operation, id, password string) {
	ctx := r.Context()
	var err error
	clearSessionCookies := true
	switch operation {
	case accountPasswordOperation:
		err = service.ChangeAccountPassword(ctx, req, password)
	case accountCodesOperation:
		codes, err := service.RegenerateRecoveryCodes(ctx, req)
		replyNewCodes(w, codes, err)
		return
	case revokeSessionOperation:
		clearSessionCookies, err = service.RevokeAccountSession(ctx, req, id)
	case revokeAllSessionsOperation:
		err = service.RevokeAll(ctx, req)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if clearSessionCookies {
		clearCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func serveFactorReplacement(w http.ResponseWriter, r *http.Request, service accountService, req identity.AuthRequest, rt route) {
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	if !utf8.Valid(raw) || !allowedObject(raw, rt.fields, false) {
		writeError(w, identity.ErrValidation)
		return
	}
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		writeError(w, identity.ErrValidation)
		return
	}
	if len(fields) == 0 {
		out, err := service.PrepareFactorReplacement(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, factorPreparationResponse{Secret: out.Secret, Token: out.ReplacementToken})
		return
	}
	if fields[replacementTokenField] == "" || fields[credentialCodeField] == "" {
		writeError(w, identity.ErrValidation)
		return
	}
	codes, err := service.CompleteFactorReplacement(r.Context(), req, fields[replacementTokenField], fields[credentialCodeField])
	replyNewCodes(w, codes, err)
}

func replyNewCodes(w http.ResponseWriter, codes []string, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	clearCookies(w)
	respond(w, recoveryCodesResponse{RecoveryCodes: codes})
}
