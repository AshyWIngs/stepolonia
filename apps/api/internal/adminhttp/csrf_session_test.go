package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"testing"
	"time"

	"stepolonia/apps/api/internal/identity"
)

type csrfSessionAuth struct {
	fakeAuth
	rotateErr error
}

func (s *csrfSessionAuth) RotateCSRF(_ context.Context, token, kind string) (identity.SessionTokens, error) {
	s.calls++
	if s.rotateErr != nil {
		return identity.SessionTokens{}, s.rotateErr
	}
	return identity.SessionTokens{Token: token, CSRF: testToken, Kind: kind, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func sessionCSRFRequest(t *testing.T, client *http.Client, origin, site string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin+authPrefix+csrfPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	setBrowserMetadata(req, site, origin+"/admin", "")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	assertPrivateNoStore(t, res)
	return res
}

func setCSRFFixtureCookie(t *testing.T, client *http.Client, host, kind string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := url.Parse(host)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: cookieNames[kind], Value: testToken, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}})
	client.Jar = jar
}

func assertCSRFSessionResponse(t *testing.T, res *http.Response, kind string) {
	t.Helper()
	if res.StatusCode != http.StatusOK {
		t.Fatalf(statusFormat, res.StatusCode)
	}
	var data map[string]string
	err := json.NewDecoder(res.Body).Decode(&data)
	if err != nil || len(data) != 2 || data["csrf_token"] != testToken || data["session_kind"] != kind {
		t.Fatalf("invalid CSRF session response: %v", err)
	}
}

func TestCSRFReportsVerifiedSessionKind(t *testing.T) {
	server := newAdminTLSServer(t, &csrfSessionAuth{})
	for _, kind := range []string{preauthKind, loginKind, fullKind, recoveryKind, enrollmentKind} {
		t.Run(kind, func(t *testing.T) {
			client := *server.Client()
			setCSRFFixtureCookie(t, &client, server.URL, kind)
			res := sessionCSRFRequest(t, &client, server.URL, sameOriginSite)
			assertCSRFSessionResponse(t, res, kind)
			if len(res.Cookies()) != 0 {
				t.Fatal("CSRF rotation rewrote the session cookie")
			}
		})
	}
}

func TestCSRFRetryAfterRevokedSession(t *testing.T) {
	service := &csrfSessionAuth{rotateErr: identity.ErrAuthenticationRequired}
	server := newAdminTLSServer(t, service)
	client := server.Client()
	setCSRFFixtureCookie(t, client, server.URL, fullKind)
	crossSite := sessionCSRFRequest(t, client, server.URL, "same-site")
	if crossSite.StatusCode != http.StatusForbidden || len(crossSite.Cookies()) != 0 || service.calls != 0 {
		t.Fatal("cross-origin request could mutate session cookies")
	}
	first := sessionCSRFRequest(t, client, server.URL, sameOriginSite)
	expired := first.Cookies()
	if first.StatusCode != http.StatusUnauthorized || len(expired) != 1 || expired[0].Name != cookieNames[fullKind] || expired[0].MaxAge >= 0 {
		t.Fatal("rejected session cookie was not expired")
	}
	retry := sessionCSRFRequest(t, client, server.URL, sameOriginSite)
	assertCSRFSessionResponse(t, retry, preauthKind)
	if !slices.ContainsFunc(retry.Cookies(), func(cookie *http.Cookie) bool {
		return cookie.Name == cookieNames[preauthKind] && cookie.Value == testToken
	}) {
		t.Fatal("preauthentication was not stored in a session cookie")
	}
}

func TestCSRFDependencyFailureRetainsSession(t *testing.T) {
	server := newAdminTLSServer(t, &csrfSessionAuth{rotateErr: identity.ErrSecurityUnavailable})
	client := server.Client()
	setCSRFFixtureCookie(t, client, server.URL, fullKind)
	res := sessionCSRFRequest(t, client, server.URL, sameOriginSite)
	if res.StatusCode != http.StatusServiceUnavailable || len(res.Cookies()) != 0 {
		t.Fatal("a dependency failure affected the browser's session")
	}
}

func TestPrivateOpenAPICSRFSessionResponse(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	get := doc.Paths[authPrefix+csrfPath]["get"]
	login := doc.Paths[authPrefix+loginPath]["post"]
	getSchema := doc.Components.Schemas[csrfSessionResponseSchema]
	postSchema := doc.Components.Schemas[csrfResponseSchema]
	wantKinds := []any{"preauth", "login", "full", "recovery", "enrollment"}
	if get.Responses["200"].Content[jsonMediaType].Schema.Ref != schemaPrefix+csrfSessionResponseSchema ||
		!slices.Equal(getSchema.Properties["session_kind"].Enum, wantKinds) || !slices.Contains(getSchema.Required, "session_kind") ||
		get.Responses["401"].Headers["Set-Cookie"] == nil {
		t.Fatal("GET CSRF contract does not describe the verified session and stale-cookie recovery")
	}
	if login.Responses["200"].Content[jsonMediaType].Schema.Ref != schemaPrefix+csrfResponseSchema || postSchema.Properties["session_kind"] != nil {
		t.Fatal("POST login response shape changed")
	}
}
