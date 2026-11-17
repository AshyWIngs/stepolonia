package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/cmshttp"
	"stepolonia/apps/api/internal/platform"
)

const qaOnboardingPassword = "Independent invitation passphrase 731!"
const qaUntrustedRequestID = "11111111-1111-4111-8111-111111111111"

// Use the real restricted LOGIN for commands; retain the owner only for assertions.
func qaOnboardingFixture(t *testing.T) authHTTPFixture {
	t.Helper()
	f := newAuthHTTPFixture(t)
	f.state.service = qaOnboardingService(t, f.state)
	return f
}

func qaOnboardingService(t *testing.T, f authFixtureState) *cms.AuthService {
	t.Helper()
	cfg := cmsDisposableDatabaseConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_auth"
	pool := Open(cfg)
	t.Cleanup(func() { _ = pool.Close() })
	requireNoError(t, CheckSecurity(context.Background(), pool))
	service, err := cms.NewAuthService(pool, f.config)
	requireNoError(t, err)
	requireNoError(t, service.CheckReady(context.Background()))
	return service
}

func qaOnboardingInvite(t *testing.T, f authFixtureState, full cms.SessionTokens, login string) cms.OnboardingToken {
	t.Helper()
	u, err := f.service.CreateUser(context.Background(), authReq(full), login)
	requireNoError(t, err)
	token, err := f.service.IssueOnboardingToken(context.Background(), authReq(full), u.ID, u.Version)
	requireNoError(t, err)
	return token
}

func qaOnboardingStart(t *testing.T, f authFixtureState, token string) cms.Enrollment {
	t.Helper()
	pre, err := f.service.NewPreauth(context.Background())
	requireNoError(t, err)
	enrollment, err := f.service.StartEnrollment(context.Background(), authReq(pre), token, qaOnboardingPassword)
	requireNoError(t, err)
	return enrollment
}

func qaOnboardingError(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func qaOnboardingStaleStep(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	_, err := db.Exec(`WITH snapshot AS (SELECT clock_timestamp() AS n) UPDATE public.sessions SET created_at=n-interval '10 minutes',last_seen=n,step_up_at=n-interval '6 minutes',expires_at=n+interval '11 hours' FROM snapshot WHERE token_digest=$1`, authDigest(token))
	requireNoError(t, err)
}

func TestAuthOnboardingIndependentCurrentAuthorityIntegration(t *testing.T) {
	f := qaOnboardingFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	_, err := f.db.Exec(`UPDATE public.roles SET name_key='Independent renamed authority' WHERE id IN (SELECT role_id FROM public.user_roles WHERE user_id=$1)`, f.user)
	requireNoError(t, err)
	_, err = f.service.CreateUser(ctx, authReq(full), "qa-allowed")
	requireNoError(t, err)
	qaOnboardingStaleStep(t, f.db, full.Token)
	_, err = f.service.CreateUser(ctx, authReq(full), "qa-stale-step")
	qaOnboardingError(t, err, cms.ErrStepUpRequired)
	_, err = f.db.Exec(`UPDATE public.sessions SET step_up_at=last_seen WHERE token_digest=$1`, authDigest(full.Token))
	requireNoError(t, err)
	lock := qaBootstrapLock(t, f.db)
	_, err = lock.Exec(`DELETE FROM public.role_grants WHERE operation='users.create' AND role_id IN (SELECT role_id FROM public.user_roles WHERE user_id=$1)`, f.user)
	requireNoError(t, err)
	result := make(chan error, 1)
	go func() {
		_, commandErr := f.service.CreateUser(ctx, authReq(full), "qa-queued-denial")
		result <- commandErr
	}()
	qaBootstrapWaiters(t, f.db, 1)
	requireNoError(t, lock.Commit())
	qaOnboardingError(t, <-result, cms.ErrPermissionDenied)
	var absent, audited bool
	requireNoError(t, f.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.users WHERE login_key IN ('qa-stale-step','qa-queued-denial')), (SELECT count(*)=2 FROM public.security_audit WHERE operation='users.created' AND result='denied' AND actor_id=$1 AND request_id IS NOT NULL)`, f.user).Scan(&absent, &audited))
	if !absent || !audited {
		t.Fatal("stale authority wrote an account or lost its separate denial audit")
	}
}

func qaOnboardingState(t *testing.T, db *sql.DB, user string) string {
	t.Helper()
	var state string
	requireNoError(t, db.QueryRow(`SELECT jsonb_build_object(
 'user',(SELECT to_jsonb(u) FROM public.users u WHERE u.id=$1),
 'tokens',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.digest) FROM public.onboarding_tokens t WHERE t.user_id=$1),
 'attempts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.session_id) FROM public.enrollment_attempts a WHERE a.user_id=$1),
 'sessions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM public.sessions s WHERE s.user_id=$1),
 'mfa',(SELECT to_jsonb(m) FROM public.mfa_totp m WHERE m.user_id=$1),
 'codes',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.digest) FROM public.recovery_codes c WHERE c.user_id=$1))::text`, user).Scan(&state))
	return state
}

func qaOnboardingRejectAudit(t *testing.T, db *sql.DB) func() {
	t.Helper()
	_, err := db.Exec(`CREATE FUNCTION public.qa_onboarding_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation IN ('users.onboarding_issued','auth.enrollment_completed') THEN RAISE EXCEPTION 'independent audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER qa_onboarding_audit_failure BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.qa_onboarding_audit_failure()`)
	requireNoError(t, err)
	active := true
	drop := func() {
		if active {
			_, dropErr := db.Exec(`DROP TRIGGER qa_onboarding_audit_failure ON public.security_audit; DROP FUNCTION public.qa_onboarding_audit_failure()`)
			requireNoError(t, dropErr)
			active = false
		}
	}
	t.Cleanup(drop)
	return drop
}

func TestAuthOnboardingIndependentReissueRollbackIntegration(t *testing.T) {
	f := qaOnboardingFixture(t).state
	ctx := context.Background()
	full := authFull(t, f)
	token := qaOnboardingInvite(t, f, full, "qa-reissue")
	enrollment := qaOnboardingStart(t, f, token.Token)
	before := qaOnboardingState(t, f.db, token.User.ID)
	restoreAudit := qaOnboardingRejectAudit(t, f.db)
	failed, err := f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, token.User.Version)
	qaOnboardingError(t, err, cms.ErrSecurityUnavailable)
	if failed.Token != "" || before != qaOnboardingState(t, f.db, token.User.ID) {
		t.Fatal("failed reissue exposed a token or changed a live enrollment")
	}
	restoreAudit()
	replacement, err := f.service.IssueOnboardingToken(ctx, authReq(full), token.User.ID, token.User.Version)
	requireNoError(t, err)
	_, err = f.service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), bootstrapCode(t, f.db, enrollment.Secret))
	qaOnboardingError(t, err, cms.ErrAuthenticationRequired)
	var invalidated bool
	requireNoError(t, f.db.QueryRow(`SELECT s.revoked_at IS NOT NULL AND a.ended_at IS NOT NULL AND u.state='pending' AND u.password_hash IS NULL FROM public.sessions s JOIN public.enrollment_attempts a ON a.session_id=s.id JOIN public.users u ON u.id=s.user_id WHERE s.token_digest=$1`, authDigest(enrollment.Token)).Scan(&invalidated))
	if !invalidated || replacement.Token == token.Token {
		t.Fatal("successful reissue retained an old session, attempt or token")
	}
	newEnrollment := qaOnboardingStart(t, f, replacement.Token)
	if newEnrollment.Token == enrollment.Token || newEnrollment.Secret == enrollment.Secret {
		t.Fatal("replacement reused the previous restricted credentials")
	}
}

func TestAuthOnboardingIndependentCompleteRollbackRaceIntegration(t *testing.T) {
	f := qaOnboardingFixture(t).state
	ctx := context.Background()
	token := qaOnboardingInvite(t, f, authFull(t, f), "qa-complete")
	enrollment := qaOnboardingStart(t, f, token.Token)
	code := bootstrapCode(t, f.db, enrollment.Secret)
	before := qaOnboardingState(t, f.db, token.User.ID)
	restoreAudit := qaOnboardingRejectAudit(t, f.db)
	codes, err := f.service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), code)
	qaOnboardingError(t, err, cms.ErrSecurityUnavailable)
	if len(codes) != 0 || before != qaOnboardingState(t, f.db, token.User.ID) {
		t.Fatal("failed completion exposed codes or changed pending credentials")
	}
	restoreAudit()
	second := qaOnboardingService(t, f)
	lock := qaBootstrapLock(t, f.db)
	type outcome struct {
		codes []string
		err   error
	}
	results := make(chan outcome, 2)
	for _, service := range []*cms.AuthService{f.service, second} {
		go func() {
			got, completeErr := service.CompleteEnrollment(ctx, authReq(enrollment.SessionTokens), code)
			results <- outcome{got, completeErr}
		}()
	}
	qaBootstrapWaiters(t, f.db, 2)
	requireNoError(t, lock.Commit())
	var succeeded int
	for range 2 {
		result := <-results
		if result.err == nil {
			succeeded++
			qaOnboardingCodes(t, f.db, token.User.ID, result.codes)
		} else {
			qaOnboardingError(t, result.err, cms.ErrAuthenticationRequired)
			if len(result.codes) != 0 {
				t.Fatal("losing completion exposed recovery codes")
			}
		}
	}
	var disabled, noAccess, singleAudit bool
	requireNoError(t, f.db.QueryRow(`SELECT state='disabled' AND enrollment_completed_at IS NOT NULL, NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=u.id) AND NOT EXISTS(SELECT 1 FROM public.sessions WHERE user_id=u.id AND revoked_at IS NULL), (SELECT count(*)=1 FROM public.security_audit WHERE operation='auth.enrollment_completed' AND target_id=u.id AND result='success') FROM public.users u WHERE id=$1`, token.User.ID).Scan(&disabled, &noAccess, &singleAudit))
	if succeeded != 1 || !disabled || !noAccess || !singleAudit {
		t.Fatal("completion race committed twice or acquired CMS access")
	}
	qaOnboardingPrivateAudit(t, f.db, []string{qaOnboardingPassword, token.Token, enrollment.Token, enrollment.CSRF, enrollment.Secret})
}

func qaOnboardingCodes(t *testing.T, db *sql.DB, user string, codes []string) {
	t.Helper()
	if len(codes) != 10 {
		t.Fatal("completion did not issue ten recovery codes")
	}
	seen := make(map[string]bool)
	for _, code := range codes {
		if seen[code] || len(code) < 32 {
			t.Fatal("recovery code is duplicated or too short")
		}
		seen[code] = true
		var stored bool
		requireNoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.recovery_codes WHERE user_id=$1 AND digest=$2 AND consumed_at IS NULL)`, user, authDigest(code)).Scan(&stored))
		if !stored {
			t.Fatal("recovery code has no matching unused digest")
		}
	}
	qaOnboardingPrivateAudit(t, db, codes)
}

func qaOnboardingPrivateAudit(t *testing.T, db *sql.DB, secrets []string) {
	t.Helper()
	var audit string
	requireNoError(t, db.QueryRow(`SELECT coalesce(string_agg(row_to_json(a)::text,''),'') FROM public.security_audit a`).Scan(&audit))
	for _, secret := range secrets {
		if secret == "" || strings.Contains(audit, secret) {
			t.Fatal("audit contains private credential material")
		}
	}
}

type qaOnboardingReply struct {
	data      []byte
	requestID string
	status    int
}

func qaOnboardingTrace(t *testing.T, f authHTTPFixture, output io.Writer) authHTTPFixture {
	t.Helper()
	var handler http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	auth, err := cmshttp.NewHandler(cmshttp.Config{CanonicalOrigin: server.URL}, f.state.service)
	requireNoError(t, err)
	handler = platform.HandlerWithAuth(nil, nil, slog.New(slog.NewJSONHandler(output, nil)), auth)
	f.server = server
	return f
}

func qaOnboardingHTTP(t *testing.T, f authHTTPFixture, path, body string, session cms.SessionTokens) qaOnboardingReply {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.server.URL+path, strings.NewReader(body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, f.server.URL, session.CSRF)
	req.Header.Set("X-Request-ID", qaUntrustedRequestID)
	req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: session.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	client := f.server.Client()
	client.Jar = nil
	res, err := client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	assertAuthNoStore(t, res)
	return qaOnboardingReply{data, authResponseRequestID(t, res), res.StatusCode}
}

func TestAuthOnboardingIndependentAuditCorrelationIntegration(t *testing.T) {
	var trace bytes.Buffer
	f := qaOnboardingTrace(t, qaOnboardingFixture(t), &trace)
	full := authFull(t, f.state)
	reply := qaOnboardingHTTP(t, f, onboardingUsersPath, `{"login":"qa-correlation"}`, full)
	var user struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	requireNoError(t, json.Unmarshal(reply.data, &user))
	qaOnboardingCorrelation(t, f.state, reply, "users.created", user.ID, "success", 200)
	var fields map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(reply.data, &fields))
	if len(fields) != 4 || fields["password_hash"] != nil || fields["roles"] != nil {
		t.Fatal("pending user DTO exposes private state")
	}
	path := onboardingUsersPath + "/" + user.ID + "/onboarding-token"
	issued := qaOnboardingHTTP(t, f, path, `{"expected_version":1}`, full)
	qaOnboardingCorrelation(t, f.state, issued, "users.onboarding_issued", user.ID, "success", 200)
	var issuedToken struct {
		Token string `json:"token"`
	}
	requireNoError(t, json.Unmarshal(issued.data, &issuedToken))
	forged := full
	forged.CSRF = strings.Repeat("A", 43)
	denied := qaOnboardingHTTP(t, f, path, `{"expected_version":2}`, forged)
	if denied.status != 403 || strings.Contains(string(denied.data), issuedToken.Token) {
		t.Fatal("forged CSRF obtained onboarding material")
	}
	qaOnboardingStaleStep(t, f.state.db, full.Token)
	denied = qaOnboardingHTTP(t, f, path, `{"expected_version":2}`, full)
	if denied.status != 403 {
		t.Fatal("stale issuer bypassed HTTP step-up")
	}
	qaOnboardingCorrelation(t, f.state, denied, "users.onboarding_issued", "", "denied", 403)
	qaOnboardingPrivateAudit(t, f.state.db, []string{full.Token, full.CSRF, issuedToken.Token})
	qaOnboardingTraceIDs(t, trace.String(), []string{reply.requestID, issued.requestID, denied.requestID}, []string{full.Token, full.CSRF, issuedToken.Token})
}

func qaOnboardingTraceIDs(t *testing.T, trace string, expected, secrets []string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(trace), "\n") {
		var event map[string]any
		requireNoError(t, json.Unmarshal([]byte(line), &event))
		if id, ok := event["request_id"].(string); ok {
			seen[id] = true
		}
	}
	for _, id := range expected {
		if !seen[id] {
			t.Fatal("HTTP log lost the response/audit correlation ID")
		}
	}
	secrets = append(secrets, qaUntrustedRequestID)
	for _, secret := range secrets {
		if strings.Contains(trace, secret) {
			t.Fatal("HTTP log contains credentials or a caller-controlled correlation ID")
		}
	}
}

func qaOnboardingCorrelation(t *testing.T, f authFixtureState, reply qaOnboardingReply, operation, target, result string, status int) {
	t.Helper()
	var id pgtype.UUID
	if id.Scan(reply.requestID) != nil || !id.Valid || reply.requestID == qaUntrustedRequestID {
		t.Fatal("response correlation is absent, malformed or caller-controlled")
	}
	if reply.status != status {
		t.Fatal("HTTP command returned an unexpected status")
	}
	var matches bool
	requireNoError(t, f.db.QueryRow(`SELECT count(*)=1 FROM public.security_audit WHERE request_id=$1 AND operation=$2 AND actor_id=$3 AND coalesce(target_id::text,'')=$4 AND result=$5`, reply.requestID, operation, f.user, target, result).Scan(&matches))
	if !matches {
		t.Fatal("response request ID does not identify its exact actor/target audit")
	}
}

func TestAuthOnboardingIndependentNearExpiryIntegration(t *testing.T) {
	f := qaOnboardingFixture(t).state
	token := qaOnboardingInvite(t, f, authFull(t, f), "qa-expiring")
	_, err := f.db.Exec(`UPDATE public.onboarding_tokens SET expires_at=clock_timestamp()+interval '2 minutes' WHERE user_id=$1`, token.User.ID)
	requireNoError(t, err)
	enrollment := qaOnboardingStart(t, f, token.Token)
	var bounded bool
	requireNoError(t, f.db.QueryRow(`SELECT s.expires_at=t.expires_at AND a.expires_at=s.expires_at AND a.expires_at<=a.created_at+interval '10 minutes' FROM public.sessions s JOIN public.enrollment_attempts a ON a.session_id=s.id JOIN public.onboarding_tokens t ON t.digest=a.token_digest WHERE s.token_digest=$1`, authDigest(enrollment.Token)).Scan(&bounded))
	var expires time.Time
	requireNoError(t, f.db.QueryRow(`SELECT expires_at FROM public.sessions WHERE token_digest=$1`, authDigest(enrollment.Token)).Scan(&expires))
	if !bounded || !enrollment.ExpiresAt.Equal(expires) {
		t.Fatal("restricted enrollment outlives its invitation")
	}
}

func TestAuthOnboardingIndependentHTTPDenialBudgetIntegration(t *testing.T) {
	f := qaOnboardingFixture(t)
	for index := range 8 {
		forged, err := f.state.service.NewPreauth(context.Background())
		requireNoError(t, err)
		// A valid random bearer of the wrong kind must not reset the address budget.
		reply := qaOnboardingHTTP(t, f, onboardingUsersPath, `{"login":"qa-no-flood-write"}`, forged)
		want := 401
		if index >= 5 {
			want = 429
		}
		if reply.status != want {
			t.Fatalf("rotating-cookie request %d returned %d, expected %d", index, reply.status, want)
		}
	}
	var bounded bool
	requireNoError(t, f.state.db.QueryRow(`SELECT (SELECT count(*) FROM public.security_audit WHERE operation='users.created' AND result='denied' AND actor_id IS NULL)=5 AND NOT EXISTS(SELECT 1 FROM public.users WHERE login_key='qa-no-flood-write') AND (SELECT coalesce(sum(count),0) FROM public.auth_attempts WHERE kind='account')=5`).Scan(&bounded))
	if !bounded {
		t.Fatal("anonymous HTTP attempts bypassed persistent audit aggregation")
	}
	full := authFull(t, f.state)
	reply := qaOnboardingHTTP(t, f, onboardingUsersPath, `{"login":"qa-authorized-after-flood"}`, full)
	if reply.status != 200 {
		t.Fatal("anonymous refusal budget blocked an authorized command")
	}
}
