package cmshttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/cms"
)

const lifecycleFixtureLogin = "safe-account"
const lifecycleFixtureState = "disabled"

type fakeLifecycle struct {
	fakeOnboarding
	page      cms.UserPage
	operation string
}

func (s *fakeLifecycle) ListUsers(_ context.Context, req cms.AuthRequest, page cms.UserPage) (cms.UserList, error) {
	s.calls++
	s.req, s.page, s.operation = req, page, listUsersOperation
	next := 4
	return cms.UserList{Items: []cms.User{{ID: exampleUUID, Login: lifecycleFixtureLogin, State: lifecycleFixtureState, Version: 7}}, NextOffset: &next}, s.commandErr
}
func (s *fakeLifecycle) GetUser(_ context.Context, req cms.AuthRequest, id string) (cms.User, error) {
	s.calls++
	s.req, s.userID, s.operation = req, id, getUserOperation
	return cms.User{ID: id, Login: lifecycleFixtureLogin, State: lifecycleFixtureState, Version: 7}, s.commandErr
}
func (s *fakeLifecycle) ActivateUser(_ context.Context, req cms.AuthRequest, id string, version int64) (cms.User, error) {
	s.calls++
	s.req, s.userID, s.version, s.operation = req, id, version, activateUserOperation
	return cms.User{ID: id, Login: "safe-account", State: "active", Version: version + 1}, s.commandErr
}
func (s *fakeLifecycle) DisableUser(_ context.Context, req cms.AuthRequest, id string, version int64) (cms.User, error) {
	s.calls++
	s.req, s.userID, s.version, s.operation = req, id, version, disableUserOperation
	return cms.User{ID: id, Login: lifecycleFixtureLogin, State: lifecycleFixtureState, Version: version + 1}, s.commandErr
}

func lifecycleGET(t *testing.T, server *httptest.Server, path, kind string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(fetchSiteHeader, sameOriginSite)
	req.Header.Set(refererHeader, server.URL+"/admin")
	if kind != "" {
		req.AddCookie(&http.Cookie{Name: cookieNames[kind], Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	return req
}

func lifecycleResponse(t *testing.T, server *httptest.Server, req *http.Request, status int) map[string]any {
	t.Helper()
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != status {
		t.Fatalf("got status %d want %d", res.StatusCode, status)
	}
	assertPrivateNoStore(t, res)
	if len(res.Cookies()) != 0 {
		t.Fatal("lifecycle changed browser cookies")
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLifecycleReadsUseFullSessionWithoutCSRF(t *testing.T) {
	service := &fakeLifecycle{}
	server := newAdminTLSServer(t, service)
	out := lifecycleResponse(t, server, lifecycleGET(t, server, usersPath+"?limit=2&offset=2", fullKind), 200)
	if service.page != (cms.UserPage{Limit: 2, Offset: 2}) || service.req.CSRF != "" || service.req.Token != testToken || service.req.RequestID == "" {
		t.Fatal("read lost page, full session or request context")
	}
	if len(out) != 2 || out["next_offset"] != float64(4) {
		t.Fatal("wrong pagination projection")
	}
	item := out["items"].([]any)[0].(map[string]any)
	assertSafeUserFields(t, item)
	out = lifecycleResponse(t, server, lifecycleGET(t, server, usersPath+"/"+exampleUUID, fullKind), 200)
	assertSafeUserFields(t, out)
	if service.operation != getUserOperation || service.userID != exampleUUID || out["state"] != "disabled" {
		t.Fatal("wrong detail target or state")
	}
}

func assertSafeUserFields(t *testing.T, item map[string]any) {
	t.Helper()
	if len(item) != 4 || item["id"] == nil || item["login"] == nil || item["state"] == nil || item["version"] == nil {
		t.Fatal("user response is not the safe projection")
	}
}

func TestLifecycleRejectsInvalidPagesBeforeService(t *testing.T) {
	service := &fakeLifecycle{}
	server := newAdminTLSServer(t, service)
	for _, query := range []string{"limit=0", "limit=101", "offset=101", "offset=-1", "limit=+1", "limit=", "limit=1&limit=2", "offset=0&offset=1", "sort=login", "state=active", "limit=1.5", "offset=9999999999999999999999", "limit=%GG", "limit=1;offset=0"} {
		t.Run(query, func(t *testing.T) {
			lifecycleResponse(t, server, lifecycleGET(t, server, usersPath+"?"+query, fullKind), 400)
		})
	}
	lifecycleResponse(t, server, lifecycleGET(t, server, usersPath+"/"+exampleUUID+"?limit=1", fullKind), 400)
	if service.calls != 0 {
		t.Fatal("invalid pagination reached service")
	}
}

func TestLifecycleReadsRejectUnsafeOriginsAndRestrictedSessions(t *testing.T) {
	service := &fakeLifecycle{}
	server := newAdminTLSServer(t, service)
	for _, kind := range []string{"", preauthKind, loginKind, recoveryKind, enrollmentKind} {
		lifecycleResponse(t, server, lifecycleGET(t, server, usersPath, kind), 401)
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del(refererHeader) },
		func(r *http.Request) { r.Header.Set(fetchSiteHeader, "cross-site") },
		func(r *http.Request) { r.Header.Set(originHeader, "https://other.example") },
		func(r *http.Request) { r.Header.Set(refererHeader, "http://"+r.Host+"/admin") },
		func(r *http.Request) { r.Header.Add(originHeader, server.URL); r.Header.Add(originHeader, server.URL) },
	} {
		req := lifecycleGET(t, server, usersPath, fullKind)
		change(req)
		lifecycleResponse(t, server, req, 403)
	}
	if service.calls != 0 {
		t.Fatal("unsafe read reached service")
	}
}

func TestLifecycleCommandsVersionAndProjection(t *testing.T) {
	service := &fakeLifecycle{}
	server := newAdminTLSServer(t, service)
	for _, action := range userStateActions {
		res := onboardingRequest(t, server, usersPath+"/"+exampleUUID+"/"+action, `{"expected_version":7}`, fullKind)
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			t.Fatal("invalid JSON")
		}
		assertSafeUserFields(t, item)
		assertVerifiedOnboardingRequest(t, service.req, res)
		if res.StatusCode != 200 || service.userID != exampleUUID || service.version != 7 || item["version"] != float64(8) {
			t.Fatal("lost target or expected version")
		}
	}
}

var userStateActions = []string{"activate", "disable"}

var invalidVersionBodies = []string{`{}`, `{"expected_version":0}`, `{"expected_version":null}`, `{"expected_version":"1"}`, `{"expected_version":1.5}`, `{"expected_version":1,"expected_version":2}`, `{"expected_version":1,"actor_id":"admin"}`, `{"expected_version":1,"state":"active"}`}

func TestLifecycleCommandsRejectInjectedAndMissingVersions(t *testing.T) {
	service := &fakeLifecycle{}
	server := newAdminTLSServer(t, service)
	for _, action := range userStateActions {
		for _, body := range invalidVersionBodies {
			res := onboardingRequest(t, server, usersPath+"/"+exampleUUID+"/"+action, body, fullKind)
			if res.StatusCode != 400 {
				t.Fatal("unsafe lifecycle body accepted", body, res.StatusCode)
			}
		}
	}
	if service.calls != 0 {
		t.Fatal("invalid command reached service")
	}
}

func TestLifecycleErrorsAndReadMethodBoundary(t *testing.T) {
	service := &fakeLifecycle{fakeOnboarding: fakeOnboarding{commandErr: cms.ErrNotFound}}
	server := newAdminTLSServer(t, service)
	out := lifecycleResponse(t, server, lifecycleGET(t, server, usersPath+"/"+exampleUUID, fullKind), 404)
	if out["code"] != "not_found" {
		t.Fatal("target refusal leaked details")
	}
	req := lifecycleGET(t, server, usersPath, fullKind)
	req.Method = http.MethodDelete
	req.Header.Set(originHeader, server.URL)
	lifecycleResponse(t, server, req, 405)
	service.commandErr = cms.ErrStepUpRequired
	res := onboardingRequest(t, server, usersPath+"/"+exampleUUID+"/disable", `{"expected_version":7}`, fullKind)
	if res.StatusCode != 403 {
		t.Fatal("lost freshness refusal")
	}
}

func TestLifecycleReadOriginAndDefaultPage(t *testing.T) {
	service := &fakeLifecycle{}
	server := newAdminTLSServer(t, service)
	req := lifecycleGET(t, server, usersPath, fullKind)
	req.Header.Del(fetchSiteHeader)
	req.Header.Del(refererHeader)
	req.Header.Set(originHeader, server.URL)
	lifecycleResponse(t, server, req, 200)
	if service.page != (cms.UserPage{Limit: 20}) {
		t.Fatal("wrong pagination defaults")
	}
	req = lifecycleGET(t, server, usersPath, fullKind)
	req.Header.Set(testCSRFHeader, strings.Repeat("x", 43))
	lifecycleResponse(t, server, req, 200)
	if service.req.CSRF != "" {
		t.Fatal("GET accidentally depends on supplied CSRF")
	}
}
