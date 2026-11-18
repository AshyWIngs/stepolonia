package adminhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"stepolonia/apps/api/internal/identity"
)

// AuthService is the private transport's authenticated transaction boundary.
type AuthService interface {
	NewPreauthFor(context.Context, identity.AuthRequest) (identity.SessionTokens, error)
	RotateCSRF(context.Context, string, string) (identity.SessionTokens, error)
	Login(context.Context, identity.AuthRequest, string, string) (identity.SessionTokens, error)
	VerifyMFA(context.Context, identity.AuthRequest, string) (identity.SessionTokens, error)
	StepUp(context.Context, identity.AuthRequest, string, string) (identity.SessionTokens, error)
	Logout(context.Context, identity.AuthRequest, string) error
	Recover(context.Context, identity.AuthRequest, string, string, string) (identity.RecoveryEnrollment, error)
	CompleteRecovery(context.Context, identity.AuthRequest, string) ([]string, error)
	CancelRecovery(context.Context, identity.AuthRequest) error
}

var _ AuthService = (*identity.AuthService)(nil)

type handler struct {
	boundary    boundary
	service     AuthService
	draftEditor DraftEditor
	settings    *identity.AuthService
}

// NewHandler validates configuration without mounting any public API routes.
func NewHandler(cfg Config, service AuthService) (http.Handler, error) {
	return NewHandlerWithDraftEditor(cfg, service, nil)
}

// NewHandlerWithDraftEditor optionally exposes the private content draft and publication routes.
func NewHandlerWithDraftEditor(cfg Config, service AuthService, editor DraftEditor) (http.Handler, error) {
	return NewHandlerWithEditors(cfg, service, editor, nil)
}

// NewHandlerWithEditors optionally enables portal settings on a verified editor login.
func NewHandlerWithEditors(cfg Config, service AuthService, editor DraftEditor, settings *identity.AuthService) (http.Handler, error) {
	b, err := newBoundary(cfg)
	if err != nil {
		return nil, err
	}
	if service == nil {
		return nil, errors.New("administration authentication service is required")
	}
	return &handler{boundary: b, service: service, draftEditor: editor, settings: settings}, nil
}

const maxCredentialBodyBytes = 4096

const credentialCodeField = "code"

type credentials struct {
	Login           string `json:"login"`
	Password        string `json:"password"`
	Code            string `json:"code"`
	Token           string `json:"token"`
	ExpectedVersion int64  `json:"expected_version"`
}

const (
	authPrefix = "/api/v1/admin/auth"
	loginPath  = "/login"
	logoutPath = "/logout"
	csrfPath   = "/csrf"
)

const (
	csrfOperation               = "authCSRF"
	logoutOperation             = "authLogout"
	recoverOperation            = "authRecover"
	completeRecoveryOperation   = "authCompleteRecovery"
	cancelRecoveryOperation     = "authCancelRecovery"
	startEnrollmentOperation    = "authStartEnrollment"
	completeEnrollmentOperation = "authCompleteEnrollment"
)

type route struct {
	roleState   roleStateCommand
	resultState string
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
	csrfPath: {kind: "", method: http.MethodGet, operationID: csrfOperation, summary: "Create or rotate a CSRF token", response: csrfSessionResponseSchema,
		description: "Without session cookies, creates a five-minute preauth session. With exactly one valid session cookie, rotates its CSRF token and returns the verified session kind without replacing the cookie. A rejected session cookie is cleared on 401 and the browser can retry once to start preauth. Start here before login or recovery; use the returned token in the next POST and retain the browser-managed cookie."},
	loginPath: {kind: preauthKind, fields: "login,password", method: http.MethodPost, operationID: "authLogin", summary: "Verify the password and begin MFA", response: csrfResponseSchema,
		description: "Requires a preauth session and its CSRF token. A successful password check replaces preauth with a five-minute restricted login session; it does not grant full access. Submit a fresh TOTP to MFA verification with the returned CSRF token."},
	"/mfa/verify": {kind: loginKind, fields: credentialCodeField, method: http.MethodPost, operationID: "authVerifyMFA", summary: "Complete login with a TOTP", response: csrfResponseSchema,
		description: "Requires a restricted login session. A valid unused TOTP replaces it with a full session and rotates CSRF. Full sessions have a 12-hour absolute lifetime, a 30-minute idle timeout and five-minute strong-authentication freshness from this successful MFA. Invalid, expired or replayed factors do not grant access."},
	"/step-up": {kind: fullKind, fields: "password,code", method: http.MethodPost, operationID: "authStepUp", summary: "Refresh strong authentication", response: csrfResponseSchema,
		description: "Requires a full session, its current CSRF token, password and a fresh TOTP. Success rotates the full session and records five-minute authentication freshness for sensitive actions, retaining the original absolute session expiry."},
	"/recovery": {kind: preauthKind, fields: "login,password,code", method: http.MethodPost, operationID: recoverOperation, summary: "Reserve recovery and enroll a replacement factor", response: "RecoveryEnrollment",
		description: "Requires preauth, password and an unused recovery code. Returns a pending TOTP seed and a ten-minute restricted recovery session, not full access. Enroll the seed locally, then confirm a fresh TOTP through recovery completion. A pending reservation prevents concurrent use; cancellation releases it."},
	"/recovery/complete": {kind: recoveryKind, fields: credentialCodeField, method: http.MethodPost, operationID: completeRecoveryOperation, summary: "Confirm the replacement factor", response: recoveryCodesSchema,
		description: "Requires the restricted recovery session and a TOTP from its pending seed. Atomically replaces factors, consumes the reserved recovery code, revokes sessions and returns ten new recovery codes once. Store them securely; browser cookies are cleared and a new login is required."},
	"/recovery/cancel": {kind: recoveryKind, method: http.MethodPost, operationID: cancelRecoveryOperation, summary: "Cancel pending factor recovery",
		description: "Requires the restricted recovery session and CSRF token. Send an empty JSON object. Ends the reservation without replacing factors, revokes the recovery session and clears browser session cookies."},
	enrollmentPath: {kind: preauthKind, fields: "token,password", method: http.MethodPost, operationID: startEnrollmentOperation, summary: "Begin the invited user's factor enrollment", response: "Enrollment",
		description: "Requires preauth and its CSRF token. A valid unexpired one-time onboarding token and a new password create a restricted enrollment session lasting at most ten minutes and bounded by the original token expiry and return its own pending TOTP seed. This consumes the token and does not grant administration access. Confirm the seed through enrollment completion before expiry; if interrupted, an authorized issuer must reissue the token."},
	enrollmentCompletePath: {kind: enrollmentKind, fields: credentialCodeField, method: http.MethodPost, operationID: completeEnrollmentOperation, summary: "Confirm the invited user's own factor", response: recoveryCodesSchema,
		description: "Requires the restricted enrollment session and a fresh TOTP from its pending seed. Success commits the password, TOTP and ten recovery codes, clears all browser session cookies and leaves the account disabled without roles or administration access. Activation and assignment require separate authorized commands."},
	logoutPath: {method: http.MethodPost, operationID: logoutOperation, summary: "Revoke the current session",
		description: "Requires exactly one session cookie of any supported kind and its CSRF token. Send an empty JSON object. Revokes that session and clears browser cookies; expired credentials return 401 and clear cookies only after the POST, JSON and CSRF-header boundary succeeds."},
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	rt, target, ok := resolveRoute(r.URL.Path, r.Method, h.draftEditor != nil)
	if !ok || settingsRoutes[target.path] != nil && h.settings == nil {
		problem(w, http.StatusNotFound, notFoundCode)
		return
	}
	ip, code := h.boundary.check(r)
	if code != "" {
		status := http.StatusForbidden
		if code == validationCode {
			status = http.StatusBadRequest
		}
		problem(w, status, code)
		return
	}
	path := target.path
	if path == csrfPath && r.Method == rt.method {
		h.csrf(w, r, ip)
		return
	}
	if path == csrfPath {
		problem(w, http.StatusNotFound, notFoundCode)
		return
	}
	if r.Method != rt.method {
		w.Header().Set("Allow", routeMethods(path))
		problem(w, http.StatusMethodNotAllowed, methodNotAllowedCode)
		return
	}
	h.serveOperation(w, r, rt, target, ip)
}

func (h *handler) serveOperation(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string) {
	path := target.path
	if settingsRoutes[path] != nil {
		h.serveSettings(w, r, rt, ip)
		return
	}
	if isRoleRoute(path) {
		h.serveRole(w, r, rt, target.id, ip)
		return
	}
	if path == userRolesPath {
		h.serveUserRoles(w, r, rt, target.id, ip)
		return
	}
	if path == contentAssigneesPath {
		h.serveContentAssignees(w, r, rt, target, ip)
		return
	}
	if path == createDraftPath {
		if rt.operationID == listDraftsOperation {
			h.serveDraftList(w, r, target, ip)
			return
		}
		h.serveDraftCreate(w, r, rt, target, ip)
		return
	}
	if _, ok := draftCandidateRoutes[path]; ok {
		h.serveDraftCandidate(w, r, rt, target, ip)
		return
	}
	if path == contentDraftPath {
		h.serveDraft(w, r, rt, target, ip)
		return
	}
	input, code, status := readRouteInput(w, r, rt)
	if code != "" {
		problem(w, status, code)
		return
	}
	h.dispatch(w, r, rt, target, ip, input)
}

type routeTarget struct{ path, id, typeKey string }

func (h *handler) dispatch(w http.ResponseWriter, r *http.Request, rt route, target routeTarget, ip string, input credentials) {
	path, userID := target.path, target.id
	req, kind, err := requestSession(r, rt.kind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	if isOnboardingRoute(path) || isLifecycleRoute(path) {
		if !attachRequestID(w, &req) {
			return
		}
	}
	if isLifecycleRoute(path) && (path != usersPath || r.Method == http.MethodGet) {
		h.executeLifecycle(w, r, rt.operationID, userID, req, input)
		return
	}
	if userID != "" || path == usersPath {
		h.executeUser(w, r, path, userID, req, input)
		return
	}
	h.execute(w, r, path, kind, req, input)
}

func attachRequestID(w http.ResponseWriter, req *identity.AuthRequest) bool {
	id, err := newRequestID()
	if err != nil {
		writeError(w, identity.ErrSecurityUnavailable)
		return false
	}
	req.RequestID = id
	w.Header().Set(requestIDHeader, id)
	return true
}

func requestSession(r *http.Request, want, ip string) (identity.AuthRequest, string, error) {
	token, kind, err := sessionCookie(r, want)
	if err != nil {
		return identity.AuthRequest{}, "", err
	}
	csrf := r.Header.Values("X-CSRF-Token")
	if r.Method != http.MethodGet && (len(csrf) != 1 || len(csrf[0]) != 43) {
		return identity.AuthRequest{}, "", identity.ErrCSRF
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return identity.AuthRequest{}, "", identity.ErrCSRF
	}
	req := identity.AuthRequest{Token: token, ClientIP: addr}
	if r.Method != http.MethodGet {
		req.CSRF = csrf[0]
	}
	return req, kind, nil
}

func newRequestID() (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	id[6], id[8] = (id[6]&15)|64, (id[8]&63)|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}

func readRouteInput(w http.ResponseWriter, r *http.Request, rt route) (credentials, string, int) {
	if rt.method == http.MethodGet {
		return credentials{}, "", 0
	}
	return readCredentials(w, r, rt.fields)
}

func readCredentials(w http.ResponseWriter, r *http.Request, allowed string) (credentials, string, int) {
	var input credentials
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		return input, code, status
	}
	if !allowedObject(raw, allowed, false) {
		return input, validationCode, http.StatusBadRequest
	}
	if json.Unmarshal(raw, &input) != nil || (allowed == expectedVersionField && input.ExpectedVersion <= 0) {
		return input, validationCode, http.StatusBadRequest
	}
	return input, "", 0
}

func readJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, string, int) {
	media, params, err := mime.ParseMediaType(r.Header.Get(contentTypeHeader))
	if err != nil || media != jsonMediaType || len(r.Header.Values(contentTypeHeader)) != 1 || !validJSONCharset(params) {
		return nil, validationCode, http.StatusBadRequest
	}
	reader := http.MaxBytesReader(w, r.Body, maxBytes)
	defer func() { _ = reader.Close() }()
	raw, err := io.ReadAll(reader)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return nil, bodyTooLargeCode, http.StatusRequestEntityTooLarge
		}
		return nil, validationCode, http.StatusBadRequest
	}
	return raw, "", 0
}

func validJSONCharset(params map[string]string) bool {
	return len(params) == 0 || len(params) == 1 && strings.EqualFold(params["charset"], "utf-8")
}

// Reject duplicate keys and unexpected value types before decoding the DTO.
func allowedObject(raw []byte, allowed string, textVersion bool) bool {
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
		if !allowedValue(dec, key, textVersion) {
			return false
		}
	}
	if token, err = dec.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	_, err = dec.Token()
	return errors.Is(err, io.EOF)
}

func allowedValue(dec *json.Decoder, key string, textVersion bool) bool {
	var value json.RawMessage
	if dec.Decode(&value) != nil || len(value) == 0 {
		return false
	}
	if key == expectedVersionField {
		if textVersion {
			return validContentVersion(value)
		}
		var version int64
		return json.Unmarshal(value, &version) == nil && version > 0
	}
	if key == roleIDsField || key == assigneeIDsField {
		return value[0] == '['
	}
	return value[0] == '"'
}

func (h *handler) execute(w http.ResponseWriter, r *http.Request, path, kind string, req identity.AuthRequest, input credentials) {
	var tokens identity.SessionTokens
	var err error
	switch path {
	case loginPath:
		tokens, err = h.service.Login(r.Context(), req, input.Login, input.Password)
	case "/mfa/verify":
		tokens, err = h.service.VerifyMFA(r.Context(), req, input.Code)
	case "/step-up":
		tokens, err = h.service.StepUp(r.Context(), req, input.Password, input.Code)
	case enrollmentPath, enrollmentCompletePath:
		h.executeEnrollment(w, r, path, req, input)
		return
	case "/recovery":
		var out identity.RecoveryEnrollment
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
		if path == logoutPath && errors.Is(err, identity.ErrAuthenticationRequired) {
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
	var out identity.SessionTokens
	if errors.Is(err, identity.ErrAuthenticationRequired) && noSessionCookies(r) {
		addr, parseErr := netip.ParseAddr(ip)
		if parseErr != nil {
			problem(w, http.StatusForbidden, originCode)
			return
		}
		out, err = h.service.NewPreauthFor(r.Context(), identity.AuthRequest{ClientIP: addr})
		if err == nil {
			setSession(w, out)
		}
	} else if err == nil {
		out, err = h.service.RotateCSRF(r.Context(), token, kind)
		if errors.Is(err, identity.ErrAuthenticationRequired) {
			clearCookie(w, cookieNames[kind])
		}
	}
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, csrfSessionResponse{CSRFToken: out.CSRF, SessionKind: out.Kind})
}

const (
	preauthKind    = "preauth"
	loginKind      = "login"
	fullKind       = "full"
	recoveryKind   = "recovery"
	enrollmentKind = "enrollment"
)

var cookieNames = map[string]string{fullKind: "__Host-sp_session", preauthKind: "__Host-sp_preauth", loginKind: "__Host-sp_login", recoveryKind: "__Host-sp_recovery", enrollmentKind: "__Host-sp_enrollment"}

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
					return "", "", identity.ErrAuthenticationRequired
				}
				token, kind = cookie.Value, k
			}
		}
	}
	if token == "" || (want != "" && kind != want) {
		return "", "", identity.ErrAuthenticationRequired
	}
	return token, kind, nil
}
func clearCookies(w http.ResponseWriter) {
	for _, name := range cookieNames {
		clearCookie(w, name)
	}
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func setSession(w http.ResponseWriter, t identity.SessionTokens) {
	clearCookies(w)
	name, ok := cookieNames[t.Kind]
	if !ok {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: t.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: t.ExpiresAt})
}
