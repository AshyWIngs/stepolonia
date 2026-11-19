package adminintegration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

const ratePolicyHTTPPath = "/api/v1/admin/security/rate-policy"

const singleAttemptPolicyUpdate = `{"scope":"credentials","expected_version":"1","window_seconds":60,"subject_attempts":1,"ip_attempts":1,"installation_attempts":1,"hash_jobs":1}`

func TestAuthRatePolicyHTTPSAuthorizationIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	full := authFull(t, f.state)
	owner := authReq(full)
	ratePolicyRequest(t, f, identity.AuthRequest{}, http.MethodGet, "", http.StatusUnauthorized)
	badCSRF := owner
	badCSRF.CSRF = strings.Repeat("a", 43)
	ratePolicyRequest(t, f, badCSRF, http.MethodPost, singleAttemptPolicyUpdate, http.StatusForbidden)
	ratePolicyRequest(t, f, owner, http.MethodPost, strings.Replace(singleAttemptPolicyUpdate, `,"hash_jobs":1`, "", 1), http.StatusBadRequest)
	_, err := f.state.db.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_rate_policy_audit
 CHECK(operation<>'security.rate_policy_changed' OR result<>'success')`)
	requireNoError(t, err)
	ratePolicyRequest(t, f, owner, http.MethodPost, singleAttemptPolicyUpdate, http.StatusServiceUnavailable)
	_, err = f.state.db.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_rate_policy_audit`)
	requireNoError(t, err)
	var updated identity.RatePolicy
	requireNoError(t, json.Unmarshal(ratePolicyRequest(t, f, owner, http.MethodPost, singleAttemptPolicyUpdate, http.StatusOK), &updated))
	if updated.Scope != "credentials" || updated.Version != 2 || updated.WindowSeconds != 60 || updated.HashJobs == nil || *updated.HashJobs != 1 {
		t.Fatalf("unexpected updated policy: %+v", updated)
	}
	ratePolicyRequest(t, f, owner, http.MethodPost, singleAttemptPolicyUpdate, http.StatusConflict)
	expireSessionStepUp(t, f.state.db, full.Token)
	ratePolicyRequest(t, f, owner, http.MethodPost, strings.Replace(singleAttemptPolicyUpdate, `"1"`, `"2"`, 1), http.StatusForbidden)
	var read struct{ Policies []identity.RatePolicy }
	requireNoError(t, json.Unmarshal(ratePolicyRequest(t, f, owner, http.MethodGet, "", http.StatusOK), &read))
	if len(read.Policies) != 4 {
		t.Fatalf("received %d policies, want 4", len(read.Policies))
	}
	for _, policy := range read.Policies {
		if policy.Scope != "credentials" && (policy.Version != 1 || policy.WindowSeconds != 900) {
			t.Fatalf("adjacent policy changed: %+v", policy)
		}
	}
	var audits int
	requireNoError(t, f.state.db.QueryRow(`SELECT count(*) FROM public.security_audit
 WHERE operation='security.rate_policy_changed' AND result='success' AND actor_id=$1 AND request_id IS NOT NULL`, f.state.user).Scan(&audits))
	if audits != 1 {
		t.Fatalf("committed %d successful policy audits, want 1", audits)
	}
}

func TestAuthRatePolicyRejectsUnprivilegedSessionIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	full := authFull(t, f.state)
	target := lifecycleEnrolled(t, f.state, full, "rate-policy-reader")
	_, err := f.state.service.ActivateUser(context.Background(), authReq(full), target.user.ID, target.user.Version)
	requireNoError(t, err)
	reader := authReq(lifecycleLoginFull(t, f.state, target))
	ratePolicyRequest(t, f, reader, http.MethodGet, "", http.StatusForbidden)
	ratePolicyRequest(t, f, reader, http.MethodPost, singleAttemptPolicyUpdate, http.StatusForbidden)
}

func TestAuthRatePolicyHTTPSRetryAndStepUpIsolationIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	ctx := context.Background()
	full := authFull(t, f.state)
	owner := authReq(full)
	ratePolicyRequest(t, f, owner, http.MethodPost, singleAttemptPolicyUpdate, http.StatusOK)
	preauth := `{"scope":"preauth","expected_version":"1","window_seconds":60,"ip_attempts":1,"installation_attempts":1}`
	ratePolicyRequest(t, f, owner, http.MethodPost, preauth, http.StatusOK)
	_, err := f.state.db.Exec(`DELETE FROM public.auth_attempt_buckets WHERE kind IN ('preauth_ip','preauth_site')`)
	requireNoError(t, err)
	pre, err := f.state.service.NewPreauthFor(ctx, identity.AuthRequest{ClientIP: f.server.Listener.Addr().(*net.TCPAddr).AddrPort().Addr()})
	requireNoError(t, err)
	_, err = f.state.service.Login(ctx, authReq(pre), bootstrapTestLogin, "incorrect password")
	if !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("first failed password did not reserve a legitimate attempt: %v", err)
	}
	_, err = f.state.service.Login(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase)
	if !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("credential attempt budget was not enforced: %v", err)
	}
	_, res := f.request(t, http.MethodGet, qaCSRFPath, "", "", http.StatusTooManyRequests)
	retry, err := strconv.Atoi(res.Header.Get("Retry-After"))
	requireNoError(t, err)
	if retry <= 0 || retry > 60 {
		t.Fatalf("Retry-After %d differs from the configured 60-second window", retry)
	}
	expireSessionStepUp(t, f.state.db, full.Token)
	requireNoError(t, resetAuthCounter(f.state))
	rotated, err := f.state.service.StepUp(ctx, owner, bootstrapTestPassphrase, bootstrapCode(t, f.state.db, f.state.secret))
	requireNoError(t, err)
	preauth = strings.Replace(preauth, `"1"`, `"2"`, 1)
	preauth = strings.Replace(preauth, `"ip_attempts":1`, `"ip_attempts":2`, 1)
	preauth = strings.Replace(preauth, `"installation_attempts":1`, `"installation_attempts":2`, 1)
	ratePolicyRequest(t, f, authReq(rotated), http.MethodPost, preauth, http.StatusOK)
	f.request(t, http.MethodGet, qaCSRFPath, "", "", http.StatusOK)
}

func TestAuthRatePolicyMissingScopeFailsReadinessIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t)
	_, err := f.state.db.Exec(`DELETE FROM public.auth_rate_policies WHERE scope='preauth'`)
	requireNoError(t, err)
	if err = f.state.service.CheckReady(context.Background()); !errors.Is(err, identity.ErrSecurityUnavailable) {
		t.Fatalf("missing rate policy should make authentication unavailable, got %v", err)
	}
}

func ratePolicyRequest(t *testing.T, f authHTTPFixture, actor identity.AuthRequest, method, body string, status int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+ratePolicyHTTPPath, strings.NewReader(body))
	requireNoError(t, err)
	return authSecurityPolicyRequest(t, f, req, actor, f.server.URL, status)
}
