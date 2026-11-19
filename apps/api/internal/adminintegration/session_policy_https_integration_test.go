package adminintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/identity"
)

const sessionPolicyHTTPPath = "/api/v1/admin/security/session-policy"
const sessionPolicyUpdate = `{"expected_version":"1","idle_timeout_minutes":45,"absolute_timeout_hours":3}`

func TestSessionPolicyHTTPSAuthorizationIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	ctx := context.Background()
	full := authFull(t, f.state)
	owner := authReq(full)
	sessionPolicyRequest(t, f, identity.AuthRequest{}, http.MethodGet, f.server.URL, "", http.StatusUnauthorized)
	sessionPolicyRequest(t, f, owner, http.MethodPut, "https://foreign.example.test", sessionPolicyUpdate, http.StatusForbidden)
	badCSRF := owner
	badCSRF.CSRF = strings.Repeat("a", 43)
	sessionPolicyRequest(t, f, badCSRF, http.MethodPut, f.server.URL, sessionPolicyUpdate, http.StatusForbidden)

	target := lifecycleEnrolled(t, f.state, full, "policy-reader")
	_, err := f.state.service.ActivateUser(ctx, owner, target.user.ID, target.user.Version)
	requireNoError(t, err)
	other := authReq(lifecycleLoginFull(t, f.state, target))
	sessionPolicyRequest(t, f, other, http.MethodGet, f.server.URL, "", http.StatusForbidden)
	sessionPolicyRequest(t, f, other, http.MethodPut, f.server.URL, sessionPolicyUpdate, http.StatusForbidden)

	expireSessionStepUp(t, f.state.db, full.Token)
	sessionPolicyRequest(t, f, owner, http.MethodPut, f.server.URL, sessionPolicyUpdate, http.StatusForbidden)
	payload := sessionPolicyRequest(t, f, owner, http.MethodGet, f.server.URL, "", http.StatusOK)
	assertSessionPolicy(t, payload, 1, 30, 12)
}

func TestSessionPolicyHTTPSLifetimeIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	ctx := context.Background()
	old := authFull(t, f.state)
	owner := authReq(old)

	for _, invalid := range []string{
		`{"expected_version":"01","idle_timeout_minutes":45,"absolute_timeout_hours":3}`,
		`{"expected_version":"1","idle_timeout_minutes":61,"absolute_timeout_hours":3}`,
		`{"expected_version":"1","idle_timeout_minutes":45,"absolute_timeout_hours":13}`,
		`{"expected_version":"1","idle_timeout_minutes":45,"absolute_timeout_hours":0}`,
		`{"expected_version":"1","idle_timeout_minutes":45,"idle_timeout_minutes":30,"absolute_timeout_hours":3}`,
	} {
		sessionPolicyRequest(t, f, owner, http.MethodPut, f.server.URL, invalid, http.StatusBadRequest)
	}

	_, err := f.state.db.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_policy_audit
 CHECK(operation<>'security.session_policy_changed' OR result<>'success')`)
	requireNoError(t, err)
	sessionPolicyRequest(t, f, owner, http.MethodPut, f.server.URL, sessionPolicyUpdate, http.StatusServiceUnavailable)
	assertSessionPolicy(t, sessionPolicyRequest(t, f, owner, http.MethodGet, f.server.URL, "", http.StatusOK), 1, 30, 12)
	_, err = f.state.db.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_policy_audit`)
	requireNoError(t, err)

	assertSessionPolicy(t, sessionPolicyRequest(t, f, owner, http.MethodPut, f.server.URL, sessionPolicyUpdate, http.StatusOK), 2, 45, 3)
	sessionPolicyRequest(t, f, owner, http.MethodPut, f.server.URL, sessionPolicyUpdate, http.StatusConflict)
	var committed int
	requireNoError(t, f.state.db.QueryRow(`SELECT count(*) FROM public.security_audit
 WHERE operation='security.session_policy_changed' AND result='success' AND actor_id=$1 AND request_id IS NOT NULL`, f.state.user).Scan(&committed))
	if committed != 1 {
		t.Fatalf("got %d committed policy changes with request IDs, want 1", committed)
	}

	assertSessionLifetime(t, f, old.Token, 30, 12*time.Hour)
	requireNoError(t, resetAuthCounter(f.state))
	rotated, err := f.state.service.StepUp(ctx, owner, bootstrapTestPassphrase, bootstrapCode(t, f.state.db, f.state.secret))
	requireNoError(t, err)
	if !rotated.ExpiresAt.Equal(old.ExpiresAt) || rotated.Token == old.Token {
		t.Fatal("step-up did not rotate the token with its original expiry")
	}
	assertSessionLifetime(t, f, rotated.Token, 30, 12*time.Hour)
	requireNoError(t, resetAuthCounter(f.state))
	fresh := authFull(t, f.state)
	assertSessionLifetime(t, f, fresh.Token, 45, 3*time.Hour)
	restricted := authLogin(t, f.state)
	assertSessionLifetime(t, f, restricted.Token, 30, 5*time.Minute)

	// Both sessions have been idle for 35 minutes, between their independent limits.
	_, err = f.state.db.Exec(`WITH stamp AS (SELECT clock_timestamp()-interval '35 minutes' AS last_seen)
 UPDATE public.sessions SET created_at=stamp.last_seen,last_seen=stamp.last_seen,step_up_at=NULL,
 expires_at=stamp.last_seen+interval '2 hours' FROM stamp WHERE token_digest IN ($1,$2)`,
		authDigest(rotated.Token), authDigest(fresh.Token))
	requireNoError(t, err)
	accountCall(t, f, authReq(rotated), http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
	accountCall(t, f, authReq(fresh), http.MethodGet, accountHTTPPath, "", http.StatusOK)

	_, err = f.state.db.Exec(`WITH stamp AS (SELECT clock_timestamp()-interval '3 hours 1 minute' AS created)
 UPDATE public.sessions SET created_at=stamp.created,last_seen=stamp.created+interval '2 hours 59 minutes',
 step_up_at=NULL,expires_at=stamp.created+interval '3 hours' FROM stamp WHERE token_digest=$1`, authDigest(fresh.Token))
	requireNoError(t, err)
	accountCall(t, f, authReq(fresh), http.MethodGet, accountHTTPPath, "", http.StatusUnauthorized)
}

func TestSessionPolicyMissingSingletonFailsAuthReadinessIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	_, err := f.state.db.Exec(`DELETE FROM public.session_policy WHERE id=1`)
	requireNoError(t, err)
	if err = f.state.service.CheckReady(context.Background()); !errors.Is(err, identity.ErrSecurityUnavailable) {
		t.Fatalf("missing session policy should make authentication unavailable, got %v", err)
	}
}

func sessionPolicyRequest(t *testing.T, f authHTTPFixture, actor identity.AuthRequest, method, origin, body string, status int) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+sessionPolicyHTTPPath, strings.NewReader(body))
	requireNoError(t, err)
	return string(authSecurityPolicyRequest(t, f, req, actor, origin, status))
}

func authSecurityPolicyRequest(t *testing.T, f authHTTPFixture, req *http.Request, actor identity.AuthRequest, origin string, status int) []byte {
	t.Helper()
	setAuthHTTPHeaders(req, origin, actor.CSRF)
	if actor.Token != "" {
		req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: actor.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	res, err := f.client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	assertAuthNoStore(t, res)
	if res.StatusCode != status {
		t.Fatalf("%s %s: received HTTP %d, want %d, body %s", req.Method, req.URL.Path, res.StatusCode, status, payload)
	}
	return payload
}

func assertSessionPolicy(t *testing.T, body string, version int64, idle, absolute int) {
	t.Helper()
	var policy identity.SessionPolicy
	requireNoError(t, json.Unmarshal([]byte(body), &policy))
	if policy.Version != version || policy.IdleTimeoutMinutes != idle || policy.AbsoluteTimeoutHours != absolute {
		t.Fatalf("unexpected policy: %s", body)
	}
}

func assertSessionLifetime(t *testing.T, f authHTTPFixture, token string, idle int, lifetime time.Duration) {
	t.Helper()
	var gotIdle int
	var gotSeconds float64
	requireNoError(t, f.state.db.QueryRow(`SELECT idle_timeout_minutes,extract(epoch FROM expires_at-created_at)
 FROM public.sessions WHERE token_digest=$1 AND revoked_at IS NULL`, authDigest(token)).Scan(&gotIdle, &gotSeconds))
	if gotIdle != idle || gotSeconds != lifetime.Seconds() {
		t.Fatalf("got idle=%d lifetime_seconds=%v, want %d and %v", gotIdle, gotSeconds, idle, lifetime.Seconds())
	}
}
