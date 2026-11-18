package adminhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"stepolonia/apps/api/internal/identity"
	"strings"
	"testing"
	"time"
)

const statusFormat = "status %d"
const testNullOrigin = "null"
const sameTestOrigin = "same"

const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeAuth struct {
	AuthService
	calls     int
	logoutErr error
	req       identity.AuthRequest
}

func (s *fakeAuth) NewPreauthFor(context.Context, identity.AuthRequest) (identity.SessionTokens, error) {
	s.calls++
	return identity.SessionTokens{Token: testToken, CSRF: testToken, Kind: preauthKind, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (s *fakeAuth) Login(_ context.Context, req identity.AuthRequest, _, _ string) (identity.SessionTokens, error) {
	s.calls++
	s.req = req
	return identity.SessionTokens{Token: testToken, CSRF: testToken, Kind: "login", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type authRequestOptions struct {
	method, path, body, origin, media, csrf string
}

func requestAuth(t *testing.T, server *httptest.Server, opts authRequestOptions) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), opts.method, server.URL+authPrefix+opts.path, strings.NewReader(opts.body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(originHeader, opts.origin)
	req.Header.Set(contentTypeHeader, opts.media)
	req.Header.Set("X-CSRF-Token", opts.csrf)
	req.AddCookie(&http.Cookie{Name: cookieNames[preauthKind], Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	req.Header.Set(forwardedForHeader, "203.0.113.99")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	assertPrivateNoStore(t, res)
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS enabled")
	}
	return res
}
func TestTransportRejectsBeforeService(t *testing.T) {
	service := &fakeAuth{}
	server := newAdminTLSServer(t, service)
	cases := []struct {
		name, path, body, origin, media, csrf string
		status                                int
	}{
		{"missing origin", loginPath, "{}", "", jsonMediaType, testToken, 403},
		{"null origin", loginPath, "{}", testNullOrigin, jsonMediaType, testToken, 403},
		{"suffix origin", loginPath, "{}", server.URL + ".evil", jsonMediaType, testToken, 403},
		{"wrong media", loginPath, "{}", server.URL, "text/plain", testToken, 400},
		{"missing csrf", loginPath, "{}", server.URL, jsonMediaType, "", 403},
		{"compound field", loginPath, `{"login,password":"ignored"}`, server.URL, jsonMediaType, testToken, 400},
		{"unknown field", loginPath, `{"roles":[]}`, server.URL, jsonMediaType, testToken, 400},
		{"duplicate key", loginPath, `{"login":"a","login":"b"}`, server.URL, jsonMediaType, testToken, 400},
		{"null credential", loginPath, `{"password":null}`, server.URL, jsonMediaType, testToken, 400},
		{"trailing value", loginPath, "{}{}", server.URL, jsonMediaType, testToken, 400},
		{"oversize", loginPath, strings.Repeat("x", maxCredentialBodyBytes+1), server.URL, jsonMediaType, testToken, 413},
		{"query", "/login?token=secret", "{}", server.URL, jsonMediaType, testToken, 400},
		{"unknown route", "/users", "{}", server.URL, jsonMediaType, testToken, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := requestAuth(t, server, authRequestOptions{
				method: http.MethodPost, path: tc.path, body: tc.body,
				origin: tc.origin, media: tc.media, csrf: tc.csrf,
			})
			if res.StatusCode != tc.status {
				t.Fatalf(statusFormat, res.StatusCode)
			}
		})
	}
	if service.calls != 0 {
		t.Fatal("invalid request reached service")
	}
}
func TestLoginCookieAndUntrustedForwardedIP(t *testing.T) {
	service := &fakeAuth{}
	server := newAdminTLSServer(t, service)
	res := requestAuth(t, server, authRequestOptions{
		method: http.MethodPost, path: loginPath, body: `{"login":"admin","password":"secret"}`,
		origin: server.URL, media: jsonMediaType, csrf: testToken,
	})
	if res.StatusCode != 200 || service.calls != 1 {
		t.Fatalf("login status %d calls %d", res.StatusCode, service.calls)
	}
	if !service.req.ClientIP.IsLoopback() {
		t.Fatal("untrusted forwarded IP accepted")
	}
	found := false
	for _, cookie := range res.Cookies() {
		if cookie.Name == cookieNames["login"] && cookie.Value != "" {
			found = true
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatalf("unsafe cookie: %+v", cookie)
			}
		}
	}
	if !found {
		t.Fatal("missing restricted login cookie")
	}
}
func TestLogoutCookieBoundary(t *testing.T) {
	cases := []struct {
		name, method, origin, csrf string
		serviceErr                 error
		status                     int
		cleared                    bool
	}{
		{"cross origin", http.MethodPost, testNullOrigin, testToken, nil, 403, false},
		{"missing origin GET", http.MethodGet, "", testToken, nil, 403, false},
		{"same origin GET", http.MethodGet, sameTestOrigin, testToken, nil, 405, false},
		{"missing CSRF", http.MethodPost, sameTestOrigin, "", nil, 403, false},
		{"forged CSRF", http.MethodPost, sameTestOrigin, testToken, identity.ErrCSRF, 403, false},
		{"expired session", http.MethodPost, sameTestOrigin, testToken, identity.ErrAuthenticationRequired, 401, true},
		{"security unavailable", http.MethodPost, sameTestOrigin, testToken, identity.ErrSecurityUnavailable, 503, false},
		{"valid logout", http.MethodPost, sameTestOrigin, testToken, nil, 204, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeAuth{logoutErr: tc.serviceErr}
			server := newAdminTLSServer(t, service)
			origin := tc.origin
			if origin == sameTestOrigin {
				origin = server.URL
			}
			res := requestAuth(t, server, authRequestOptions{
				method: tc.method, path: logoutPath, body: "{}",
				origin: origin, media: jsonMediaType, csrf: tc.csrf,
			})
			if res.StatusCode != tc.status {
				t.Fatalf(statusFormat, res.StatusCode)
			}
			assertLogoutCookies(t, res, tc.cleared)
		})
	}
}

func assertLogoutCookies(t *testing.T, res *http.Response, cleared bool) {
	t.Helper()
	if !cleared {
		if len(res.Cookies()) != 0 {
			t.Fatal("denied logout changed browser cookies")
		}
		return
	}
	if len(res.Cookies()) != len(cookieNames) {
		t.Fatal("logout did not clear all cookies")
	}
	for _, cookie := range res.Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatal("cookie not deleted")
		}
	}
}

func (s *fakeAuth) Logout(context.Context, identity.AuthRequest, string) error {
	s.calls++
	return s.logoutErr
}

func TestCanonicalOriginAndProxyTrust(t *testing.T) {
	for _, origin := range []string{"http://admin.test", "https://admin.test/", "https://user@admin.test", "https://admin.test?", "https://admin.test#x", "https://ADMIN.test", "https://admin.test:", "https://admin.test:65536"} {
		if _, err := NewHandler(Config{CanonicalOrigin: origin}, &fakeAuth{}); err == nil {
			t.Errorf("accepted %q", origin)
		}
	}
	b, err := newBoundary(Config{CanonicalOrigin: "https://admin.test", TrustedProxyCIDRs: []string{"192.0.2.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://admin.test"+authPrefix+"/csrf", nil)
	req.TLS = nil
	req.RemoteAddr = "192.0.2.5:1234"
	req.Header.Set(originHeader, "https://admin.test")
	req.Header.Set("X-Forwarded-Proto", httpsScheme)
	req.Header.Set(forwardedForHeader, "203.0.113.4")
	ip, code := b.check(req)
	if code != "" || ip != "203.0.113.4" {
		t.Fatalf("trusted proxy failed %s %s", ip, code)
	}
	req.Header.Set(forwardedForHeader, "203.0.113.4, 192.0.2.6")
	if _, code = b.check(req); code == "" {
		t.Fatal("ambiguous proxy chain accepted")
	}
	req.RemoteAddr = "198.51.100.2:1234"
	if _, code = b.check(req); code == "" {
		t.Fatal("untrusted proxy enabled cleartext")
	}
}

func TestCSRFRequiresSameOriginAndBootstrapsRestrictedSession(t *testing.T) {
	service := &fakeAuth{}
	server := newAdminTLSServer(t, service)
	for _, origin := range []string{"", server.URL} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+authPrefix+"/csrf", nil)
		if err != nil {
			t.Fatal(err)
		}
		if origin != "" {
			req.Header.Set(originHeader, origin)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		want := http.StatusForbidden
		if origin != "" {
			want = http.StatusOK
		}
		if res.StatusCode != want {
			t.Fatal(res.StatusCode)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cached CSRF")
		}
	}
	if service.calls != 1 {
		t.Fatal("cross-origin GET reached service")
	}
}

func TestRestrictedCookieCannotCrossRoute(t *testing.T) {
	service := &fakeAuth{}
	server := newAdminTLSServer(t, service)
	for _, name := range []string{cookieNames[fullKind], cookieNames[recoveryKind]} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+authPrefix+loginPath, strings.NewReader(`{"login":"admin","password":"secret"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(originHeader, server.URL)
		req.Header.Set(contentTypeHeader, jsonMediaType)
		req.Header.Set("X-CSRF-Token", testToken)
		req.AddCookie(&http.Cookie{Name: name, Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatal("wrong session kind reached login")
		}
	}
	if service.calls != 0 {
		t.Fatal("restricted kind bypass")
	}
}

func TestBrowserCSRFMetadata(t *testing.T) {
	service := &fakeAuth{}
	server := newAdminTLSServer(t, service)
	cases := []struct {
		name, site, referer, origin string
		status                      int
	}{
		{"same origin fetch", sameOriginSite, server.URL + "/admin", "", 200},
		{"cross site", "cross-site", server.URL + "/admin", "", 403},
		{"same site", "same-site", server.URL + "/admin", "", 403},
		{"navigation", "none", server.URL + "/admin", "", 403},
		{"missing site", "", server.URL + "/admin", "", 403},
		{"missing referer", sameOriginSite, "", "", 403},
		{"wrong referer", sameOriginSite, "https://evil.test/admin", "", 403},
		{"malformed referer", sameOriginSite, ":invalid", "", 403},
		{"null origin cannot fallback", sameOriginSite, server.URL + "/admin", testNullOrigin, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+authPrefix+"/csrf", nil)
			if err != nil {
				t.Fatal(err)
			}
			setBrowserMetadata(req, tc.site, tc.referer, tc.origin)
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf(statusFormat, res.StatusCode)
			}
		})
	}
	if service.calls != 1 {
		t.Fatal("invalid metadata reached service")
	}
}

func setBrowserMetadata(req *http.Request, site, referer, origin string) {
	for key, value := range map[string]string{"Sec-Fetch-Site": site, "Referer": referer, originHeader: origin} {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
}

func assertPrivateNoStore(t *testing.T, res *http.Response) {
	t.Helper()
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("response permits caching")
	}
}
