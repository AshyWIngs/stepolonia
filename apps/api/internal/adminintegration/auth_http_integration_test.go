package adminintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"stepolonia/apps/api/internal/identity"
	"testing"
	"time"
)

const authFullSessionCookie = "__Host-sp_session"

const authRequestIDHeader = "X-Request-ID"

func TestAuthHTTPDedicatedPoolIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	pre, _ := f.request(t, http.MethodGet, qaCSRFPath, "", "", 200)
	credentials, err := json.Marshal(map[string]string{"login": bootstrapTestLogin, "password": bootstrapTestPassphrase})
	requireNoError(t, err)
	f.request(t, http.MethodPost, "/api/v1/admin/auth/login", "", string(credentials), 403)
	challenge, _ := f.request(t, http.MethodPost, "/api/v1/admin/auth/login", pre["csrf_token"], string(credentials), 200)
	code := bootstrapCode(t, f.state.db, f.state.secret)
	full, res := f.request(t, http.MethodPost, "/api/v1/admin/auth/mfa/verify", challenge["csrf_token"], `{"code":"`+code+`"}`, 200)
	var bearer string
	for _, cookie := range res.Cookies() {
		if cookie.Name == authFullSessionCookie && cookie.Value != "" {
			bearer = cookie.Value
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	if bearer == "" {
		t.Fatal("MFA did not issue full session")
	}
	req := identity.AuthRequest{Token: bearer, CSRF: full["csrf_token"]}
	requireNoError(t, f.state.service.WithSession(context.Background(), req, true, authAllow))
	f.request(t, http.MethodPost, "/api/v1/admin/content/article", "", "{}", 404)
	f.request(t, http.MethodPost, "/api/v1/admin/auth/logout", full["csrf_token"], "{}", 204)
	authDenied(t, f.state.service.WithSession(context.Background(), req, false, authAllow))
}

func setAuthHTTPHeaders(req *http.Request, origin, csrf string) {
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
}

func assertAuthNoStore(t *testing.T, res *http.Response) {
	t.Helper()
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("authentication response permits caching")
	}
}

func requireAuthBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("ADMIN_HTTP_BROWSER_TEST") != "1" {
		t.Skip("NOT RUN: opt-in local Chromium required")
	}
}

func authResponseRequestID(t *testing.T, res *http.Response) string {
	t.Helper()
	id := res.Header.Get("X-Request-ID")
	if id == "" {
		t.Fatal("authentication response lacks server correlation identifier")
	}
	return id
}

func assertAuthCookiesCleared(t *testing.T, res *http.Response) {
	t.Helper()
	expected := map[string]bool{
		authFullSessionCookie:  true,
		"__Host-sp_preauth":    true,
		"__Host-sp_login":      true,
		"__Host-sp_recovery":   true,
		"__Host-sp_enrollment": true,
	}
	if len(res.Cookies()) != len(expected) {
		t.Fatal("logout did not clear each supported authentication cookie")
	}
	for _, cookie := range res.Cookies() {
		if !expected[cookie.Name] {
			t.Fatal("logout returned an unknown or duplicate cookie")
		}
		delete(expected, cookie.Name)
		if cookie.Value != "" || cookie.MaxAge != -1 || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" || cookie.Expires.After(time.Now()) {
			t.Fatal("logout cookie deletion lacks required security attributes")
		}
	}
}
