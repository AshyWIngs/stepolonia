package cmsintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/pquerna/otp/totp"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"stepolonia/apps/api/internal/cms"
	"strings"
	"testing"
	"time"
)

const qaSessionAgeSQL = `UPDATE public.sessions SET created_at=created_at-interval '10 hours',expires_at=expires_at-interval '10 hours' WHERE token_digest=$1`
const qaContentTypeHeader = "Content-Type"
const qaUnknownLogin = "unknown@example.test"
const qaCSRFPath = "/api/v1/admin/auth/csrf"
const qaLoginPath = "/api/v1/admin/auth/login"

func TestAuthIndependentAbsoluteLifetimeIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	_, err := f.db.Exec(qaSessionAgeSQL, authDigest(full.Token))
	requireNoError(t, err)
	var created, expiry, now time.Time
	requireNoError(t, f.db.QueryRow(`SELECT created_at,expires_at,clock_timestamp() FROM public.sessions WHERE token_digest=$1`, authDigest(full.Token)).Scan(&created, &expiry, &now))
	code, err := totp.GenerateCode(f.secret, now.Add(30*time.Second))
	requireNoError(t, err)
	stepped, err := f.service.StepUp(ctx, authReq(full), bootstrapTestPassphrase, code)
	requireNoError(t, err)
	if !stepped.ExpiresAt.Equal(expiry) {
		t.Fatal("step-up extended absolute lifetime")
	}
	var same bool
	requireNoError(t, f.db.QueryRow(`SELECT created_at=$2 AND expires_at=$3 FROM public.sessions WHERE token_digest=$1`, authDigest(stepped.Token), created, expiry).Scan(&same))
	if !same {
		t.Fatal("rotation reset original session creation")
	}
	crossed := authReq(stepped)
	crossed.CSRF = full.CSRF
	if err := f.service.WithSession(ctx, crossed, true, authAllow); !errors.Is(err, cms.ErrCSRF) {
		t.Fatalf("old CSRF survived rotation: %v", err)
	}
	_, err = f.db.Exec(`UPDATE public.sessions SET created_at=CURRENT_TIMESTAMP-interval '12 hours 1 minute',expires_at=CURRENT_TIMESTAMP-interval '1 minute',last_seen=CURRENT_TIMESTAMP-interval '2 minutes',step_up_at=NULL WHERE token_digest=$1`, authDigest(stepped.Token))
	requireNoError(t, err)
	called := false
	err = f.service.WithSession(ctx, authReq(stepped), false, func(context.Context, *sql.Tx, cms.Session) error { called = true; return nil })
	if !errors.Is(err, cms.ErrAuthenticationRequired) || called {
		t.Fatal("recent activity revived an absolutely expired session")
	}
}

func TestAuthIndependentRevocationAfterMutexIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	full := authFull(t, f)
	lock := holdInstallationWriteMutex(t, f.db)
	_, err := lock.Exec(`UPDATE public.sessions SET revoked_at=clock_timestamp() WHERE token_digest=$1`, authDigest(full.Token))
	requireNoError(t, err)
	result := make(chan error, 1)
	called := make(chan struct{}, 1)
	go func() {
		result <- f.service.WithSession(ctx, authReq(full), true, func(context.Context, *sql.Tx, cms.Session) error { called <- struct{}{}; return nil })
	}()
	waitForInstallationWriteWaiters(t, f.db, 1)
	requireNoError(t, lock.Commit())
	if err := <-result; !errors.Is(err, cms.ErrAuthenticationRequired) {
		t.Fatalf("queued request accepted revoked session: %v", err)
	}
	select {
	case <-called:
		t.Fatal("revoked request executed protected callback")
	default:
	}
}

func TestAuthIndependentLastCodeCancelRollbackIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	_, err := f.db.Exec(`UPDATE public.recovery_codes SET consumed_at=clock_timestamp() WHERE digest<>$1`, authDigest(f.codes[0]))
	requireNoError(t, err)
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	recovery, err := f.service.Recover(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[0])
	requireNoError(t, err)
	_, err = f.db.Exec(`CREATE FUNCTION public.qa_cancel_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='mfa.recovery_cancelled' THEN RAISE EXCEPTION 'qa audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER qa_cancel_audit_fail BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.qa_cancel_audit_fail()`)
	requireNoError(t, err)
	if err := f.service.CancelRecovery(ctx, authReq(recovery.SessionTokens)); !errors.Is(err, cms.ErrSecurityUnavailable) {
		t.Fatalf("cancel audit did not fail closed: %v", err)
	}
	var reserved bool
	requireNoError(t, f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.recovery_attempts a JOIN public.sessions s ON s.id=a.session_id JOIN public.recovery_codes c ON c.digest=a.code_digest WHERE s.token_digest=$1 AND s.revoked_at IS NULL AND a.ended_at IS NULL AND c.consumed_at IS NULL)`, authDigest(recovery.Token)).Scan(&reserved))
	if !reserved {
		t.Fatal("failed cancel lost last-code reservation or revoked recovery")
	}
	_, err = f.db.Exec(`DROP TRIGGER qa_cancel_audit_fail ON public.security_audit; DROP FUNCTION public.qa_cancel_audit_fail()`)
	requireNoError(t, err)
	requireNoError(t, f.service.CancelRecovery(ctx, authReq(recovery.SessionTokens)))
	_, err = f.service.CompleteRecovery(ctx, authReq(recovery.SessionTokens), bootstrapCode(t, f.db, recovery.Secret))
	if !errors.Is(err, cms.ErrAuthenticationRequired) {
		t.Fatalf("cancelled enrollment remained usable: %v", err)
	}
	pre, err = f.service.NewPreauth(ctx)
	requireNoError(t, err)
	next, err := f.service.Recover(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase, f.codes[0])
	requireNoError(t, err)
	if next.Secret == recovery.Secret {
		t.Fatal("cancelled seed was reissued")
	}
	codes, err := f.service.CompleteRecovery(ctx, authReq(next.SessionTokens), bootstrapCode(t, f.db, next.Secret))
	requireNoError(t, err)
	if len(codes) != 10 {
		t.Fatal("last-code recovery did not replace recovery set")
	}
}

func TestAuthIndependentTLSCSRFAndUnknownLimitsIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	first, _ := f.request(t, http.MethodGet, qaCSRFPath, "", "", 200)
	second, _ := f.request(t, http.MethodGet, qaCSRFPath, "", "", 200)
	if first["csrf_token"] == second["csrf_token"] {
		t.Fatal("CSRF GET failed to rotate synchronizer secret")
	}
	body, err := qaLoginJSON("UNKNOWN@EXAMPLE.TEST")
	requireNoError(t, err)
	f.request(t, http.MethodPost, qaLoginPath, first["csrf_token"], string(body), 403)
	for i := 0; i < 5; i++ {
		login := qaUnknownLogin
		if i%2 == 0 {
			login = strings.ToUpper(login)
		}
		body, err = qaLoginJSON(login)
		requireNoError(t, err)
		qaSpoofedLogin(t, f, second["csrf_token"], string(body), http.StatusUnauthorized)
	}
	qaSpoofedLogin(t, f, second["csrf_token"], string(body), http.StatusTooManyRequests)
	var safe bool
	requireNoError(t, f.state.db.QueryRow(`SELECT sum(count)=5 AND bool_and(key_digest<>$1) FROM public.auth_attempts WHERE kind='ip'`, authDigest("203.0.113.123")).Scan(&safe))
	if !safe {
		t.Fatal("untrusted forwarding header changed persistent client-IP budget")
	}
	var budgets int
	requireNoError(t, f.state.db.QueryRow(`SELECT sum(count) FROM public.auth_attempts WHERE kind='account' AND key_digest=$1`, authDigest(qaUnknownLogin)).Scan(&budgets))
	if budgets != 5 {
		t.Fatal("unknown account failures were not normalized/persisted")
	}
}

func qaLoginJSON(login string) ([]byte, error) {
	return json.Marshal(map[string]string{"login": login, "password": bootstrapTestPassphrase})
}

func qaSpoofedLogin(t *testing.T, f authHTTPFixture, csrf, body string, status int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.server.URL+qaLoginPath, strings.NewReader(body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, f.server.URL, csrf)
	req.Header.Set("X-Forwarded-For", "203.0.113.123")
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := f.client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	if res.StatusCode != status {
		t.Fatalf("unexpected unknown-login HTTP status %d", res.StatusCode)
	}
	if status == http.StatusTooManyRequests && res.Header.Get("Retry-After") == "" {
		t.Fatal("rate limit missing Retry-After")
	}
	if strings.Contains(string(raw), bootstrapTestPassphrase) || strings.Contains(string(raw), qaUnknownLogin) {
		t.Fatal("authentication error reflected credentials")
	}
}

func TestAuthIndependentCrossSiteLogoutBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(qaContentTypeHeader, "text/html")
		_, _ = io.WriteString(w, `<!doctype html><a id="logout" href="`+f.server.URL+`/api/v1/admin/auth/logout">Open link</a>`)
	}))
	defer attacker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodeExecutable, "-e", qaCrossSiteLogoutScript)
	command.Env = append(os.Environ(), "QA_AUTH_ORIGIN="+f.server.URL, "QA_ATTACKER_ORIGIN="+attacker.URL, "QA_AUTH_BEARER="+full.Token)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("cross-site logout Chromium scenario failed: %v: %s", err, output)
	}
	requireNoError(t, f.state.service.WithSession(context.Background(), authReq(full), false, authAllow))
}

const qaCrossSiteLogoutScript = `
const { chromium } = require('../../../web/node_modules/playwright');
(async () => {
 const browser = await chromium.launch({headless:true});
 try {
  const origin = process.env.QA_AUTH_ORIGIN;
  const context = await browser.newContext({ignoreHTTPSErrors:true});
  await context.addCookies([{name:'__Host-sp_session',value:process.env.QA_AUTH_BEARER,url:origin,secure:true,httpOnly:true,sameSite:'Strict'}]);
  const page = await context.newPage();
  await page.goto(process.env.QA_ATTACKER_ORIGIN);
  const [response] = await Promise.all([page.waitForNavigation(),page.locator('#logout').click()]);
  if (response.status() !== 403) throw new Error('cross-site logout was not rejected');
  const cookies = await context.cookies(origin);
  if (!cookies.some(c => c.name === '__Host-sp_session' && c.value === process.env.QA_AUTH_BEARER)) throw new Error('cross-site rejection cleared victim session cookie');
 } finally {await browser.close();}
})().catch(error => {console.error(error.message);process.exitCode=1;});
`

func TestAuthIndependentExpiredLogoutCookiesIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	_, err := f.state.db.Exec(`UPDATE public.sessions SET created_at=CURRENT_TIMESTAMP-interval '12 hours 1 minute',expires_at=CURRENT_TIMESTAMP-interval '1 minute',last_seen=CURRENT_TIMESTAMP-interval '2 minutes',step_up_at=NULL WHERE token_digest=$1`, authDigest(full.Token))
	requireNoError(t, err)
	target, err := url.Parse(f.server.URL)
	requireNoError(t, err)
	f.client.Jar.SetCookies(target, []*http.Cookie{{Name: authFullSessionCookie, Value: full.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}})
	_, res := f.request(t, http.MethodPost, "/api/v1/admin/auth/logout", full.CSRF, "{}", http.StatusUnauthorized)
	assertAuthCookiesCleared(t, res)
	if len(f.client.Jar.Cookies(target)) != 0 {
		t.Fatal("validated expired logout retained browser authentication cookies")
	}
}

func TestAuthIndependentLoginAuditRollbackIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	qaAuthRejectAudit(t, f.db)
	login, err := f.service.Login(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase)
	if !errors.Is(err, cms.ErrSecurityUnavailable) || login.Token != "" || login.CSRF != "" {
		t.Fatal("failed login audit committed or returned credentials")
	}
	qaAuthRestoreAudit(t, f.db)
	login, err = f.service.Login(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	code := bootstrapCode(t, f.db, f.secret)
	qaAuthRejectAudit(t, f.db)
	full, err := f.service.VerifyMFA(ctx, authReq(login), code)
	if !errors.Is(err, cms.ErrSecurityUnavailable) || full.Token != "" || full.CSRF != "" {
		t.Fatal("failed MFA audit committed or returned full credentials")
	}
	var fullSessions int
	requireNoError(t, f.db.QueryRow(`SELECT count(*) FROM public.sessions WHERE kind='full'`).Scan(&fullSessions))
	if fullSessions != 0 {
		t.Fatal("failed MFA audit left a full session")
	}
	qaAuthRestoreAudit(t, f.db)
	full, err = f.service.VerifyMFA(ctx, authReq(login), code)
	requireNoError(t, err)
	requireNoError(t, f.service.WithSession(ctx, authReq(full), true, authAllow))
}

func qaAuthRejectAudit(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE FUNCTION public.qa_auth_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation IN ('auth.login_started','auth.login_completed') THEN RAISE EXCEPTION 'qa audit unavailable'; END IF; RETURN NEW; END $$;
CREATE TRIGGER qa_auth_reject_audit BEFORE INSERT ON public.security_audit FOR EACH ROW EXECUTE FUNCTION public.qa_auth_reject_audit()`)
	requireNoError(t, err)
}

func qaAuthRestoreAudit(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`DROP TRIGGER qa_auth_reject_audit ON public.security_audit; DROP FUNCTION public.qa_auth_reject_audit()`)
	requireNoError(t, err)
}
