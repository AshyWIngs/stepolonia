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

const (
	originCode         = "origin_denied"
	validationCode     = "validation_failed"
	preauthKind        = "preauth"
	codeField          = "code"
	recoveryKind       = "recovery"
	logoutPath         = "/logout"
	contentTypeHeader  = "Content-Type"
	csrfField          = "csrf_token"
	forwardedForHeader = "X-Forwarded-For"
	loginPath          = "/login"

	maxBody  = 4096
	jsonType = "application/json"
	fullKind = "full"
	csrfCode = "csrf_failed"
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

type credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

type route struct {
	kind   string
	fields string
}

var routes = map[string]route{
	loginPath:            {preauthKind, "login,password"},
	"/mfa/verify":        {"login", codeField},
	"/step-up":           {fullKind, "password,code"},
	"/recovery":          {preauthKind, "login,password,code"},
	"/recovery/complete": {recoveryKind, codeField},
	"/recovery/cancel":   {recoveryKind, ""},
	logoutPath:           {"", ""},
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !strings.HasPrefix(r.URL.Path, authPrefix+"/") {
		problem(w, http.StatusNotFound, "not_found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, authPrefix)
	if path == logoutPath {
		clearCookies(w)
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
	if path == "/csrf" && r.Method == http.MethodGet {
		h.csrf(w, r, ip)
		return
	}
	rt, ok := routes[path]
	if !ok {
		problem(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
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
	if err != nil || media != jsonType || len(r.Header.Values(contentTypeHeader)) != 1 || (len(params) > 0 && (len(params) != 1 || !strings.EqualFold(params["charset"], "utf-8"))) {
		return input, validationCode, http.StatusBadRequest
	}
	reader := http.MaxBytesReader(w, r.Body, maxBody)
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
			respond(w, map[string]string{csrfField: out.CSRF, "secret": out.Secret})
			return
		}
	case "/recovery/complete":
		var codes []string
		codes, err = h.service.CompleteRecovery(r.Context(), req, input.Code)
		if err == nil {
			clearCookies(w)
			respond(w, map[string][]string{"recovery_codes": codes})
			return
		}
	case "/recovery/cancel":
		err = h.service.CancelRecovery(r.Context(), req)
	case logoutPath:
		err = h.service.Logout(r.Context(), req, kind)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if tokens.Token != "" {
		setSession(w, tokens)
		respond(w, map[string]string{csrfField: tokens.CSRF})
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
	respond(w, map[string]string{csrfField: out.CSRF})
}

var cookieNames = map[string]string{fullKind: "__Host-sp_session", preauthKind: "__Host-sp_preauth", "login": "__Host-sp_login", recoveryKind: "__Host-sp_recovery"}

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
func respond(w http.ResponseWriter, value any) {
	w.Header().Set(contentTypeHeader, jsonType)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set(contentTypeHeader, "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, codeField: code, "title": http.StatusText(status)})
}
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "security_unavailable"
	switch {
	case errors.Is(err, cms.ErrInvalidCredentials):
		status, code = http.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, cms.ErrAuthenticationRequired):
		status, code = http.StatusUnauthorized, "authentication_required"
	case errors.Is(err, cms.ErrCSRF):
		status, code = http.StatusForbidden, csrfCode
	case errors.Is(err, cms.ErrRateLimited):
		status, code = http.StatusTooManyRequests, "rate_limited"
		w.Header().Set("Retry-After", "900")
	}
	problem(w, status, code)
}
