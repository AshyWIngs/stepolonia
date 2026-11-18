package cmshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

const pendingUserFixtureState = "pending"
const enrolledCodeJSON = `{"code":"012345"}`
const enrollmentCookieOracle = "__Host-sp_enrollment"
const testCSRFHeader = "X-CSRF-Token"

type fakeOnboarding struct {
	fakeAuth
	commandErr error
	userID     string
	version    int64
}

func (s *fakeOnboarding) CreateUser(_ context.Context, req cms.AuthRequest, login string) (cms.User, error) {
	s.calls++
	s.req = req
	return cms.User{ID: exampleUUID, Login: login, State: pendingUserFixtureState, Version: 1}, s.commandErr
}
func (s *fakeOnboarding) IssueOnboardingToken(_ context.Context, req cms.AuthRequest, id string, version int64) (cms.OnboardingToken, error) {
	s.calls++
	s.req, s.userID, s.version = req, id, version
	return cms.OnboardingToken{Token: testToken, ExpiresAt: time.Now().Add(24 * time.Hour), User: cms.User{ID: id, Login: "invited-editor", State: pendingUserFixtureState, Version: version + 1}}, s.commandErr
}
func (s *fakeOnboarding) StartEnrollment(_ context.Context, req cms.AuthRequest, token, password string) (cms.Enrollment, error) {
	s.calls++
	s.req = req
	if req.CSRF != testToken {
		return cms.Enrollment{}, cms.ErrCSRF
	}
	if token != testToken || password == "" {
		return cms.Enrollment{}, cms.ErrInvalidCredentials
	}
	return cms.Enrollment{SessionTokens: cms.SessionTokens{Token: testToken, CSRF: testToken, Kind: enrollmentKind, ExpiresAt: time.Now().Add(10 * time.Minute)}, Secret: strings.Repeat("A", 32)}, s.commandErr
}
func (s *fakeOnboarding) CompleteEnrollment(_ context.Context, req cms.AuthRequest, code string) ([]string, error) {
	s.calls++
	s.req = req
	if req.CSRF != testToken {
		return nil, cms.ErrCSRF
	}
	if code != "012345" {
		return nil, cms.ErrInvalidCredentials
	}
	return []string{strings.Repeat("0", 32)}, s.commandErr
}
func (s *fakeOnboarding) RotateCSRF(_ context.Context, token, kind string) (cms.SessionTokens, error) {
	s.calls++
	return cms.SessionTokens{Token: token, CSRF: testToken, Kind: kind, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func onboardingRequest(t *testing.T, server *httptest.Server, path, body, kind string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(originHeader, server.URL)
	req.Header.Set(contentTypeHeader, jsonMediaType)
	req.Header.Set(testCSRFHeader, testToken)
	req.AddCookie(&http.Cookie{Name: cookieNames[kind], Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	assertPrivateNoStore(t, res)
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("private command lost response security headers")
	}
	return res
}

func TestOnboardingTLSBoundary(t *testing.T) {
	service := &fakeOnboarding{}
	server := newAdminTLSServer(t, service)
	cases := []struct {
		name, path, body, kind string
		status                 int
	}{
		{"actor injection", usersPath, `{"login":"x","actor_id":"admin"}`, fullKind, 400},
		{"permission injection", usersPath, `{"login":"x","permissions":["users.create"]}`, fullKind, 400},
		{"role assignment", usersPath, `{"login":"x","role":"admin"}`, fullKind, 400},
		{"state assignment", usersPath, `{"login":"x","state":"active"}`, fullKind, 400},
		{"duplicate login", usersPath, `{"login":"x","login":"y"}`, fullKind, 400},
		{"missing version", usersPath + "/" + exampleUUID + "/onboarding-token", `{}`, fullKind, 400},
		{"string version", usersPath + "/" + exampleUUID + "/onboarding-token", `{"expected_version":"1"}`, fullKind, 400},
		{"null version", usersPath + "/" + exampleUUID + "/onboarding-token", `{"expected_version":null}`, fullKind, 400},
		{"fractional version", usersPath + "/" + exampleUUID + "/onboarding-token", `{"expected_version":1.5}`, fullKind, 400},
		{"overflow version", usersPath + "/" + exampleUUID + "/onboarding-token", `{"expected_version":9223372036854775808}`, fullKind, 400},
		{"negative version", usersPath + "/" + exampleUUID + "/onboarding-token", `{"expected_version":-1}`, fullKind, 400},
		{"duplicate version", usersPath + "/" + exampleUUID + "/onboarding-token", `{"expected_version":1,"expected_version":2}`, fullKind, 400},
		{"restricted create", usersPath, `{"login":"x"}`, enrollmentKind, 401},
		{"preauth create", usersPath, `{"login":"x"}`, preauthKind, 401},
		{"full enrollment start", authPrefix + enrollmentPath, `{}`, fullKind, 401},
		{"recovery enrollment complete", authPrefix + enrollmentCompletePath, enrolledCodeJSON, recoveryKind, 401},
		{"login enrollment complete", authPrefix + enrollmentCompletePath, enrolledCodeJSON, loginKind, 401},
		{"cross-user enrollment", authPrefix + enrollmentCompletePath, `{"code":"012345","user_id":"other"}`, enrollmentKind, 400},
		{"token URL", authPrefix + enrollmentPath + "?token=secret", `{}`, preauthKind, 400},
		{"extra route segment", usersPath + "/one/two/onboarding-token", `{}`, fullKind, 404},
		{"unknown lifecycle command", usersPath + "/" + exampleUUID + "/delete", `{}`, fullKind, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := onboardingRequest(t, server, tc.path, tc.body, tc.kind)
			if res.StatusCode != tc.status {
				t.Fatalf("status %d want %d", res.StatusCode, tc.status)
			}
			if len(res.Cookies()) != 0 {
				t.Fatal("rejected request changed session cookies")
			}
		})
	}
	if service.calls != 0 {
		t.Fatal("invalid input or restricted routing reached service")
	}
}

func TestOnboardingCommandsUseVerifiedSessionAndPrivateDTO(t *testing.T) {
	service := &fakeOnboarding{}
	server := newAdminTLSServer(t, service)
	res := onboardingRequest(t, server, usersPath, `{"login":"invited-editor"}`, fullKind)
	var user pendingUserResponse
	if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || user.State != "pending" || user.Version != 1 || len(res.Cookies()) != 0 {
		t.Fatal("pending creation lost verified request context or changed session", res.StatusCode)
	}
	assertVerifiedOnboardingRequest(t, service.req, res)
	res = onboardingRequest(t, server, usersPath+"/"+user.ID+"/onboarding-token", `{"expected_version":1}`, fullKind)
	var out onboardingTokenResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || service.userID != user.ID || service.version != 1 || out.Token == "" || out.User.Version != 2 || len(res.Cookies()) != 0 {
		t.Fatal("token issuance lost target/version or changed session")
	}
}

func TestOnboardingServiceErrorsPreserveHTTPMeaning(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{cms.ErrAuthenticationRequired, 401, authenticationCode},
		{cms.ErrPermissionDenied, 403, "permission_denied"},
		{cms.ErrStepUpRequired, 403, "step_up_required"},
		{cms.ErrVersionConflict, 409, "version_conflict"},
		{cms.ErrInvalidTransition, 409, "invalid_transition"},
		{cms.ErrNotFound, 404, "not_found"},
		{cms.ErrValidation, 400, validationCode},
		{cms.ErrCSRF, 403, csrfCode},
		{cms.ErrRateLimited, 429, "rate_limited"},
		{errors.New("database failed with private details"), 503, "security_unavailable"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			service := &fakeOnboarding{commandErr: tc.err}
			server := newAdminTLSServer(t, service)
			res := onboardingRequest(t, server, usersPath+"/"+exampleUUID+"/onboarding-token", `{"expected_version":1}`, fullKind)
			var out authProblem
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status || out.Code != tc.code || len(res.Cookies()) != 0 {
				t.Fatal("wrong failure mapping", res.StatusCode, out.Code)
			}
			if tc.status == 429 && res.Header.Get("Retry-After") != "900" {
				t.Fatal("rate limit lost retry delay")
			}
		})
	}
}

func TestEnrollmentSessionIsRestrictedAndCompletionClearsCookies(t *testing.T) {
	service := &fakeOnboarding{}
	server := newAdminTLSServer(t, service)
	res := onboardingRequest(t, server, authPrefix+enrollmentPath, `{"token":"`+testToken+`","password":"new secret passphrase"}`, preauthKind)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	set := 0
	for _, cookie := range res.Cookies() {
		if cookie.MaxAge != -1 {
			set++
			if cookie.Name != enrollmentCookieOracle || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatal("unsafe or elevated enrollment cookie")
			}
		}
	}
	if set != 1 {
		t.Fatal("enrollment did not replace other cookies")
	}
	for _, path := range []string{usersPath, authPrefix + "/step-up", authPrefix + "/mfa/verify", authPrefix + "/recovery/complete"} {
		res = onboardingRequest(t, server, path, `{}`, enrollmentKind)
		if res.StatusCode != 401 {
			t.Fatal("enrollment escaped restricted allowlist", path, res.StatusCode)
		}
	}
	res = onboardingRequest(t, server, authPrefix+enrollmentCompletePath, enrolledCodeJSON, enrollmentKind)
	if res.StatusCode != 200 {
		t.Fatal("completion failed", res.StatusCode)
	}
	assertLogoutCookies(t, res, true)
}

func TestEnrollmentMixedCookiesFailClosed(t *testing.T) {
	service := &fakeOnboarding{}
	server := newAdminTLSServer(t, service)
	for _, name := range []string{cookieNames[fullKind], cookieNames[enrollmentKind]} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+authPrefix+enrollmentCompletePath, strings.NewReader(enrolledCodeJSON))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(originHeader, server.URL)
		req.Header.Set(contentTypeHeader, jsonMediaType)
		req.Header.Set(testCSRFHeader, testToken)
		req.AddCookie(&http.Cookie{Name: cookieNames[enrollmentKind], Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		req.AddCookie(&http.Cookie{Name: name, Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 401 || service.calls != 0 {
			t.Fatal("mixed or duplicate enrollment cookies accepted")
		}
	}
}

func TestEnrollmentCSRFAndLogoutRetainKind(t *testing.T) {
	service := &fakeOnboarding{}
	server := newAdminTLSServer(t, service)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+authPrefix+"/csrf", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(originHeader, server.URL)
	req.AddCookie(&http.Cookie{Name: enrollmentCookieOracle, Value: testToken, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	assertCSRFSessionResponse(t, res, enrollmentKind)
	_ = res.Body.Close()
	if len(res.Cookies()) != 0 {
		t.Fatal("CSRF rotation replaced the restricted enrollment cookie")
	}
	res = onboardingRequest(t, server, authPrefix+logoutPath, `{}`, enrollmentKind)
	if res.StatusCode != 204 {
		t.Fatal("restricted logout failed")
	}
	assertLogoutCookies(t, res, true)
}

func assertVerifiedOnboardingRequest(t *testing.T, req cms.AuthRequest, res *http.Response) {
	t.Helper()
	if req.Token != testToken || req.CSRF != testToken || req.RequestID == "" || req.RequestID != res.Header.Get("X-Request-ID") || !req.ClientIP.IsLoopback() {
		t.Fatal("onboarding request lost verified session or audit correlation")
	}
}
