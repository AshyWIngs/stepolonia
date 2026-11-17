package database

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/cmshttp"
	"stepolonia/apps/api/internal/platform"
)

type authHTTPFixture struct {
	state  authFixtureState
	server *httptest.Server
	client *http.Client
}

func newAuthHTTPFixture(t *testing.T) authHTTPFixture {
	t.Helper()
	f := authFixture(t)
	cfg := cmsDisposableDatabaseConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	role := cfg.Database + "_auth"
	quoted := pgx.Identifier{role}.Sanitize()
	_, err := f.db.Exec("CREATE ROLE " + quoted + " LOGIN IN ROLE sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = f.db.Exec("DROP OWNED BY " + quoted); _, _ = f.db.Exec("DROP ROLE " + quoted) })
	_, err = f.db.Exec("REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " FROM PUBLIC")
	requireNoError(t, err)
	cfg.User = role
	db := Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	requireNoError(t, CheckSecurity(context.Background(), db))
	service, err := cms.NewAuthService(db, f.config)
	requireNoError(t, err)
	requireNoError(t, service.CheckReady(context.Background()))
	var handler http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	auth, err := cmshttp.NewHandler(cmshttp.Config{CanonicalOrigin: server.URL}, service)
	requireNoError(t, err)
	handler = platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	client := server.Client()
	client.Jar, err = cookiejar.New(nil)
	requireNoError(t, err)
	return authHTTPFixture{f, server, client}
}

func (f authHTTPFixture) request(t *testing.T, method, path, csrf, body string, status int) (map[string]string, *http.Response) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	requireNoError(t, err)
	req.Header.Set("Origin", f.server.URL)
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	res, err := f.client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	if res.StatusCode != status {
		t.Fatalf("%s %s status=%d body=%s", method, path, res.StatusCode, data)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("authentication response permits caching")
	}
	out := map[string]string{}
	if status == http.StatusOK {
		requireNoError(t, json.Unmarshal(data, &out))
	}
	return out, res
}

func TestAuthHTTPDedicatedPoolIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	pre, _ := f.request(t, http.MethodGet, "/api/v1/admin/auth/csrf", "", "", 200)
	credentials, err := json.Marshal(map[string]string{"login": bootstrapTestLogin, "password": bootstrapTestPassphrase})
	requireNoError(t, err)
	f.request(t, http.MethodPost, "/api/v1/admin/auth/login", "", string(credentials), 403)
	challenge, _ := f.request(t, http.MethodPost, "/api/v1/admin/auth/login", pre["csrf_token"], string(credentials), 200)
	code := bootstrapCode(t, f.state.db, f.state.secret)
	full, res := f.request(t, http.MethodPost, "/api/v1/admin/auth/mfa/verify", challenge["csrf_token"], `{"code":"`+code+`"}`, 200)
	var bearer string
	for _, cookie := range res.Cookies() {
		if cookie.Name == "__Host-sp_session" && cookie.Value != "" {
			bearer = cookie.Value
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	if bearer == "" {
		t.Fatal("MFA did not issue full session")
	}
	req := cms.AuthRequest{Token: bearer, CSRF: full["csrf_token"]}
	requireNoError(t, f.state.service.WithSession(context.Background(), req, true, authAllow))
	f.request(t, http.MethodPost, "/api/v1/admin/content/article", "", "{}", 404)
	f.request(t, http.MethodPost, "/api/v1/admin/auth/logout", full["csrf_token"], "{}", 204)
	authDenied(t, f.state.service.WithSession(context.Background(), req, false, authAllow))
}
