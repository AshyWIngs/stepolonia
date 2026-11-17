package cmshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"stepolonia/apps/api/internal/cms"
)

// AuthService is the private transport's authenticated transaction boundary.
type AuthService interface {
	NewPreauthFor(context.Context, cms.AuthRequest) (cms.SessionTokens, error)
	RotateCSRF(context.Context, string, string) (cms.SessionTokens, error)
	Login(context.Context, cms.AuthRequest, string, string) (cms.SessionTokens, error)
	VerifyMFA(context.Context, cms.AuthRequest, string) (cms.SessionTokens, error)
	StepUp(context.Context, cms.AuthRequest, string, string) (cms.SessionTokens, error)
	Logout(context.Context, cms.AuthRequest, string) error
	Recover(context.Context, cms.AuthRequest, string, string, string) (cms.RecoveryEnrollment, error)
	CompleteRecovery(context.Context, cms.AuthRequest, string) ([]string, error)
	CancelRecovery(context.Context, cms.AuthRequest) error
}

var _ AuthService = (*cms.AuthService)(nil)

type handler struct {
	boundary boundary
	service  AuthService
}

// NewHandler validates configuration without mounting any public API routes.
func NewHandler(cfg Config, service AuthService) (http.Handler, error) {
	b, err := newBoundary(cfg)
	if err != nil {
		return nil, err
	}
	if service == nil {
		return nil, errors.New("CMS auth service is required")
	}
	return &handler{boundary: b, service: service}, nil
}

const maxCredentialBodyBytes = 4096

const credentialCodeField = "code"

type credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

const (
	authPrefix = "/api/v1/admin/auth"
	loginPath  = "/login"
	logoutPath = "/logout"
)

const logoutOperation = "authLogout"

type route struct {
	kind        string
	fields      string
	method      string
	operationID string
	summary     string
	description string
	response    string
}

// Route metadata is shared by dispatch and the private OpenAPI generator.
var routes = map[string]route{
	"/csrf": {kind: "", method: http.MethodGet, operationID: "authCSRF", summary: "Create or rotate a CSRF token", response: csrfResponseSchema,
		description: "Without session cookies, creates a five-minute preauth session. With exactly one valid session cookie, rotates its CSRF token while retaining its session kind. Start here before login or recovery; use the returned token in the next POST and retain the browser-managed cookie."},
	loginPath: {kind: preauthKind, fields: "login,password", method: http.MethodPost, operationID: "authLogin", summary: "Verify the password and begin MFA", response: csrfResponseSchema,
		description: "Requires a preauth session and its CSRF token. A successful password check replaces preauth with a five-minute restricted login session; it does not grant full access. Submit a fresh TOTP to MFA verification with the returned CSRF token."},
	"/mfa/verify": {kind: loginKind, fields: credentialCodeField, method: http.MethodPost, operationID: "authVerifyMFA", summary: "Complete login with a TOTP", response: csrfResponseSchema,
		description: "Requires a restricted login session. A valid unused TOTP replaces it with a full session and rotates CSRF. Full sessions have a 12-hour absolute lifetime, a 30-minute idle timeout and five-minute strong-authentication freshness from this successful MFA. Invalid, expired or replayed factors do not grant access."},
	"/step-up": {kind: fullKind, fields: "password,code", method: http.MethodPost, operationID: "authStepUp", summary: "Refresh strong authentication", response: csrfResponseSchema,
		description: "Requires a full session, its current CSRF token, password and a fresh TOTP. Success rotates the full session and records five-minute authentication freshness for sensitive actions, retaining the original absolute session expiry."},
	"/recovery": {kind: preauthKind, fields: "login,password,code", method: http.MethodPost, operationID: "authRecover", summary: "Reserve recovery and enroll a replacement factor", response: "RecoveryEnrollment",
		description: "Requires preauth, password and an unused recovery code. Returns a pending TOTP seed and a ten-minute restricted recovery session, not full access. Enroll the seed locally, then confirm a fresh TOTP through recovery completion. A pending reservation prevents concurrent use; cancellation releases it."},
	"/recovery/complete": {kind: recoveryKind, fields: credentialCodeField, method: http.MethodPost, operationID: "authCompleteRecovery", summary: "Confirm the replacement factor", response: "RecoveryCodes",
		description: "Requires the restricted recovery session and a TOTP from its pending seed. Atomically replaces factors, consumes the reserved recovery code, revokes sessions and returns ten new recovery codes once. Store them securely; browser cookies are cleared and a new login is required."},
	"/recovery/cancel": {kind: recoveryKind, method: http.MethodPost, operationID: "authCancelRecovery", summary: "Cancel pending factor recovery",
		description: "Requires the restricted recovery session and CSRF token. Send an empty JSON object. Ends the reservation without replacing factors, revokes the recovery session and clears browser session cookies."},
	logoutPath: {method: http.MethodPost, operationID: logoutOperation, summary: "Revoke the current session",
		description: "Requires exactly one session cookie of any supported kind and its CSRF token. Send an empty JSON object. Revokes that session and clears browser cookies; expired credentials return 401 and clear cookies only after the POST, JSON and CSRF-header boundary succeeds."},
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !strings.HasPrefix(r.URL.Path, authPrefix+"/") {
		problem(w, http.StatusNotFound, "not_found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, authPrefix)
	ip, code := h.boundary.check(r)
	if code != "" {
		status := http.StatusForbidden
		if code == validationCode {
			status = http.StatusBadRequest
		}
		problem(w, status, code)
		return
	}
	rt, ok := routes[path]
	if ok && rt.method == http.MethodGet && r.Method == rt.method {
		h.csrf(w, r, ip)
		return
	}
	if !ok || rt.method == http.MethodGet {
		problem(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != rt.method {
		problem(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	input, code, status := readCredentials(w, r, rt.fields)
	if code != "" {
		problem(w, status, code)
		return
	}
	token, kind, err := sessionCookie(r, rt.kind)
	if err != nil {
		writeError(w, err)
		return
	}
	csrf := r.Header.Values("X-CSRF-Token")
	if len(csrf) != 1 || len(csrf[0]) != 43 {
		problem(w, http.StatusForbidden, csrfCode)
		return
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		problem(w, http.StatusForbidden, originCode)
		return
	}
	req := cms.AuthRequest{Token: token, CSRF: csrf[0], ClientIP: addr}
	h.execute(w, r, path, kind, req, input)
}

func readCredentials(w http.ResponseWriter, r *http.Request, allowed string) (credentials, string, int) {
	var input credentials
	media, params, err := mime.ParseMediaType(r.Header.Get(contentTypeHeader))
	if err != nil || media != jsonMediaType || len(r.Header.Values(contentTypeHeader)) != 1 || (len(params) > 0 && (len(params) != 1 || !strings.EqualFold(params["charset"], "utf-8"))) {
		return input, validationCode, http.StatusBadRequest
	}
	reader := http.MaxBytesReader(w, r.Body, maxCredentialBodyBytes)
	defer func() { _ = reader.Close() }()
	raw, err := io.ReadAll(reader)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return input, "body_too_large", http.StatusRequestEntityTooLarge
		}
		return input, validationCode, http.StatusBadRequest
	}
	if !allowedObject(raw, allowed) {
		return input, validationCode, http.StatusBadRequest
	}
	if json.Unmarshal(raw, &input) != nil {
		return input, validationCode, http.StatusBadRequest
	}
	return input, "", 0
}

// Reject duplicate keys and non-string credentials before decoding the DTO.
func allowedObject(raw []byte, allowed string) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool)
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] || key == "" || !slices.Contains(strings.Split(allowed, ","), key) {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if dec.Decode(&value) != nil || len(value) == 0 || value[0] != '"' {
			return false
		}
	}
	if token, err = dec.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	_, err = dec.Token()
	return errors.Is(err, io.EOF)
}

func (h *handler) execute(w http.ResponseWriter, r *http.Request, path, kind string, req cms.AuthRequest, input credentials) {
	var tokens cms.SessionTokens
	var err error
	switch path {
	case loginPath:
		tokens, err = h.service.Login(r.Context(), req, input.Login, input.Password)
	case "/mfa/verify":
		tokens, err = h.service.VerifyMFA(r.Context(), req, input.Code)
	case "/step-up":
		tokens, err = h.service.StepUp(r.Context(), req, input.Password, input.Code)
	case "/recovery":
		var out cms.RecoveryEnrollment
		out, err = h.service.Recover(r.Context(), req, input.Login, input.Password, input.Code)
		if err == nil {
			setSession(w, out.SessionTokens)
			respond(w, recoveryEnrollmentResponse{CSRFToken: out.CSRF, Secret: out.Secret})
			return
		}
	case "/recovery/complete":
		var codes []string
		codes, err = h.service.CompleteRecovery(r.Context(), req, input.Code)
		if err == nil {
			clearCookies(w)
			respond(w, recoveryCodesResponse{RecoveryCodes: codes})
			return
		}
	case "/recovery/cancel":
		err = h.service.CancelRecovery(r.Context(), req)
	case logoutPath:
		err = h.service.Logout(r.Context(), req, kind)
	}
	if err != nil {
		// Expired credentials may clear browser state only after the request
		// passed the same-origin POST, JSON and CSRF header boundary.
		if path == logoutPath && errors.Is(err, cms.ErrAuthenticationRequired) {
			clearCookies(w)
		}
		writeError(w, err)
		return
	}
	if tokens.Token != "" {
		setSession(w, tokens)
		respond(w, csrfResponse{CSRFToken: tokens.CSRF})
		return
	}
	clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) csrf(w http.ResponseWriter, r *http.Request, ip string) {
	token, kind, err := sessionCookie(r, "")
	var out cms.SessionTokens
	if errors.Is(err, cms.ErrAuthenticationRequired) && noSessionCookies(r) {
		addr, parseErr := netip.ParseAddr(ip)
		if parseErr != nil {
			problem(w, http.StatusForbidden, originCode)
			return
		}
		out, err = h.service.NewPreauthFor(r.Context(), cms.AuthRequest{ClientIP: addr})
	} else if err == nil {
		out, err = h.service.RotateCSRF(r.Context(), token, kind)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	setSession(w, out)
	respond(w, csrfResponse{CSRFToken: out.CSRF})
}

const (
	preauthKind  = "preauth"
	loginKind    = "login"
	fullKind     = "full"
	recoveryKind = "recovery"
)

var cookieNames = map[string]string{fullKind: "__Host-sp_session", preauthKind: "__Host-sp_preauth", loginKind: "__Host-sp_login", recoveryKind: "__Host-sp_recovery"}

func noSessionCookies(r *http.Request) bool {
	for _, cookie := range r.Cookies() {
		for _, name := range cookieNames {
			if cookie.Name == name {
				return false
			}
		}
	}
	return true
}
func sessionCookie(r *http.Request, want string) (string, string, error) {
	token, kind := "", ""
	for _, cookie := range r.Cookies() {
		for k, name := range cookieNames {
			if cookie.Name == name {
				if token != "" || len(cookie.Value) != 43 {
					return "", "", cms.ErrAuthenticationRequired
				}
				token, kind = cookie.Value, k
			}
		}
	}
	if token == "" || (want != "" && kind != want) {
		return "", "", cms.ErrAuthenticationRequired
	}
	return token, kind, nil
}
func clearCookies(w http.ResponseWriter) {
	for _, name := range cookieNames {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	}
}
func setSession(w http.ResponseWriter, t cms.SessionTokens) {
	clearCookies(w)
	name, ok := cookieNames[t.Kind]
	if !ok {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: t.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: t.ExpiresAt})
}
