package adminintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

const (
	accountHTTPPath         = "/api/v1/admin/me"
	accountSessionsHTTPPath = "/api/v1/admin/sessions"
	accountAuditHTTPPath    = "/api/v1/admin/audit"
)

func accountCall(t *testing.T, f authHTTPFixture, actor identity.AuthRequest, method, path, body string, status int) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, f.server.URL, actor.CSRF)
	req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: actor.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	response, err := f.client.Do(req)
	requireNoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assertAuthNoStore(t, response)
	if response.StatusCode != status {
		t.Fatalf("%s %s: received HTTP %d, want %d", method, path, response.StatusCode, status)
	}
	payload, err := io.ReadAll(response.Body)
	requireNoError(t, err)
	return string(payload)
}

func TestAdminAccountSessionsAndAuditHTTPSIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	actor := authReq(authFull(t, f.state))
	requireNoError(t, resetAuthCounter(f.state))
	other := authReq(authFull(t, f.state))

	accountCall(t, f, actor, http.MethodGet, accountHTTPPath, "", http.StatusOK)
	out := accountCall(t, f, actor, http.MethodGet, accountSessionsHTTPPath, "", http.StatusOK)
	var page struct {
		Items []struct {
			ID      string
			Current bool
		}
	}
	requireNoError(t, json.Unmarshal([]byte(out), &page))
	if len(page.Items) != 2 || page.Items[0].Current || !page.Items[1].Current {
		t.Fatalf("unexpected session projection: %s", out)
	}
	for _, token := range []string{actor.Token, actor.CSRF, other.Token, other.CSRF} {
		if strings.Contains(out, token) {
			t.Fatal("session projection disclosed credential")
		}
	}
	expireSessionStepUp(t, f.state.db, actor.Token)
	accountCall(t, f, actor, http.MethodPost, accountSessionsHTTPPath+"/revoke-all", "{}", http.StatusForbidden)
	accountCall(t, f, actor, http.MethodPost, accountSessionsHTTPPath+"/"+page.Items[0].ID+"/revoke", "{}", http.StatusNoContent)
	accountCall(t, f, other, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
	audit := accountCall(t, f, actor, http.MethodGet, accountAuditHTTPPath+"?limit=4", "", http.StatusOK)
	if !strings.Contains(audit, `"operation":"auth.session_revoked"`) || strings.Contains(audit, `"details"`) {
		t.Fatalf("unexpected audit projection: %s", audit)
	}
	accountCall(t, f, actor, http.MethodGet, accountAuditHTTPPath+"?limit=101", "", http.StatusBadRequest)
	authRefreshStep(t, f.state.db, actor.Token)
	accountCall(t, f, actor, http.MethodPost, accountSessionsHTTPPath+"/revoke-all", "{}", http.StatusNoContent)
	accountCall(t, f, actor, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
}

func TestAdminAccountCredentialReplacementHTTPSIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	actor := authReq(authFull(t, f.state))
	factorPath := accountHTTPPath + "/mfa/replace"
	codesPath := accountHTTPPath + "/recovery-codes"
	accountCall(t, f, actor, http.MethodPost, factorPath, `{"replacement_token":""}`, http.StatusBadRequest)
	body := accountCall(t, f, actor, http.MethodPost, factorPath, "{}", http.StatusOK)
	var prep struct {
		Token  string `json:"replacement_token"`
		Secret string
	}
	requireNoError(t, json.Unmarshal([]byte(body), &prep))
	if prep.Secret == "" || prep.Secret == f.state.secret || prep.Token == "" {
		t.Fatal("missing replacement credentials")
	}

	requireNoError(t, resetAuthCounter(f.state))
	other := authReq(authFull(t, f.state))
	input := fmt.Sprintf(`{"replacement_token":%q,"code":%q}`, prep.Token, bootstrapCode(t, f.state.db, prep.Secret))
	accountCall(t, f, other, http.MethodPost, factorPath, input, http.StatusUnauthorized)
	_, err := f.state.db.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_factor_audit CHECK(operation<>'mfa.replaced' OR result<>'success')`)
	requireNoError(t, err)
	accountCall(t, f, actor, http.MethodPost, factorPath, input, http.StatusServiceUnavailable)
	accountCall(t, f, actor, http.MethodGet, accountHTTPPath, "", http.StatusOK)
	_, err = f.state.db.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_factor_audit`)
	requireNoError(t, err)
	replaced := accountCall(t, f, actor, http.MethodPost, factorPath, input, http.StatusOK)
	assertAccountRecoveryCodes(t, replaced, f.state.codes)
	accountCall(t, f, actor, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
	accountCall(t, f, other, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)

	requireNoError(t, resetAuthCounter(f.state))
	f.state.secret = prep.Secret
	replacement := authReq(authFull(t, f.state))
	const newPassword = "Another secure password example 2026!"
	passwordBody := fmt.Sprintf(`{"password":%q}`, newPassword)
	accountCall(t, f, replacement, http.MethodPost, accountHTTPPath+"/password", passwordBody, http.StatusNoContent)
	accountCall(t, f, replacement, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
	ctx := context.Background()
	pre, err := f.state.service.NewPreauth(ctx)
	requireNoError(t, err)
	if _, err := f.state.service.Login(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("previous password was accepted: %v", err)
	}
	login, err := f.state.service.Login(ctx, authReq(pre), bootstrapTestLogin, newPassword)
	requireNoError(t, err)
	requireNoError(t, resetAuthCounter(f.state))
	full, err := f.state.service.VerifyMFA(ctx, authReq(login), bootstrapCode(t, f.state.db, prep.Secret))
	requireNoError(t, err)
	newActor := authReq(full)
	codes := accountCall(t, f, newActor, http.MethodPost, codesPath, "{}", http.StatusOK)
	assertAccountRecoveryCodes(t, codes, f.state.codes)
	if codes == replaced {
		t.Fatal("regeneration reused the previous recovery codes")
	}
	accountCall(t, f, newActor, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
}

func TestAdminAccountForeignSessionHTTPSIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	actor := authReq(full)
	target := lifecycleEnrolled(t, f.state, full, "session-review-account")
	_, err := f.state.service.ActivateUser(context.Background(), actor, target.user.ID, target.user.Version)
	requireNoError(t, err)
	other := authReq(lifecycleLoginFull(t, f.state, target))
	targetPath := "/api/v1/admin/users/" + target.user.ID + "/sessions"
	accountCall(t, f, other, http.MethodGet, accountAuditHTTPPath, "", http.StatusForbidden)
	accountCall(t, f, other, http.MethodGet, "/api/v1/admin/users/"+f.state.user+"/sessions", "", http.StatusNotFound)
	expireSessionStepUp(t, f.state.db, actor.Token)
	accountCall(t, f, actor, http.MethodGet, targetPath, "", http.StatusForbidden)
	authRefreshStep(t, f.state.db, actor.Token)
	payload := accountCall(t, f, actor, http.MethodGet, targetPath, "", http.StatusOK)
	var sessions struct {
		Items []struct {
			ID      string
			Current bool
		}
	}
	requireNoError(t, json.Unmarshal([]byte(payload), &sessions))
	if len(sessions.Items) != 1 || sessions.Items[0].Current {
		t.Fatal("target session page is incorrect")
	}
	path := accountSessionsHTTPPath + "/" + sessions.Items[0].ID + "/revoke"
	expireSessionStepUp(t, f.state.db, actor.Token)
	accountCall(t, f, actor, http.MethodPost, path, "{}", http.StatusForbidden)
	authRefreshStep(t, f.state.db, actor.Token)
	accountCall(t, f, actor, http.MethodPost, path, "{}", http.StatusNoContent)
	accountCall(t, f, other, http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
}

func resetAuthCounter(state authFixtureState) error {
	_, err := state.db.Exec(previousTOTPCounterSQL, state.user)
	return err
}

func assertAccountRecoveryCodes(t *testing.T, body string, previous []string) {
	t.Helper()
	var out struct {
		Codes []string `json:"recovery_codes"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &out))
	if len(out.Codes) != 10 {
		t.Fatal("expected ten new recovery codes")
	}
	for _, old := range previous {
		if strings.Contains(body, old) {
			t.Fatal("previous recovery code was returned")
		}
	}
}
