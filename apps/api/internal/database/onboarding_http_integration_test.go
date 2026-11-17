package database

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

const (
	onboardingIssuerCookie     = "session"
	onboardingPreauthCookie    = "preauth"
	onboardingRestrictedCookie = "enrollment"
)

const onboardingUsersPath = "/api/v1/admin/users"
const onboardingStartPath = "/api/v1/admin/auth/enrollment"
const onboardingCompletePath = onboardingStartPath + "/complete"
const onboardingPassword = "Invited account passphrase 42!"

type onboardingUserHTTP struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Version int64  `json:"version"`
}

type onboardingTokenHTTP struct {
	Token string             `json:"token"`
	User  onboardingUserHTTP `json:"user"`
}

func (f authHTTPFixture) onboardingHTTP(t *testing.T, path, body, kind string, session cms.SessionTokens, status int, out any) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.server.URL+path, strings.NewReader(body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, f.server.URL, session.CSRF)
	req.AddCookie(&http.Cookie{Name: "__Host-sp_" + kind, Value: session.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	client := f.server.Client()
	client.Jar = nil
	res, err := client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != status {
		// Secret-bearing success bodies are never included in diagnostic output.
		t.Fatalf("private onboarding status=%d expected=%d", res.StatusCode, status)
	}
	assertAuthNoStore(t, res)
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("private onboarding response lost security headers")
	}
	if out != nil {
		requireNoError(t, json.NewDecoder(res.Body).Decode(out))
	} else {
		_, err = io.Copy(io.Discard, res.Body)
		requireNoError(t, err)
	}
	return res
}

func TestAuthOnboardingHTTPIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	for _, body := range []string{`{"login":"invite-http","actor_id":"admin"}`, `{"login":"invite-http","state":"active"}`, `{"login":"invite-http","permissions":["users.create"]}`} {
		f.onboardingHTTP(t, onboardingUsersPath, body, onboardingIssuerCookie, full, 400, nil)
	}
	var user onboardingUserHTTP
	f.onboardingHTTP(t, onboardingUsersPath, `{"login":"invite-http"}`, onboardingIssuerCookie, full, 200, &user)
	if user.State != "pending" || user.Version != 1 {
		t.Fatal("creation did not return a pending user")
	}
	path := onboardingUsersPath + "/" + user.ID + "/onboarding-token"
	var first, second onboardingTokenHTTP
	f.onboardingHTTP(t, path, onboardingVersionJSON(t, 1), onboardingIssuerCookie, full, 200, &first)
	f.onboardingHTTP(t, path, onboardingVersionJSON(t, 1), onboardingIssuerCookie, full, 409, nil)
	f.onboardingHTTP(t, path, onboardingVersionJSON(t, 2), onboardingIssuerCookie, full, 200, &second)
	if second.Token == first.Token || second.User.Version != 3 {
		t.Fatal("reissue retained the old token or account version")
	}
	pre, err := f.state.service.NewPreauth(context.Background())
	requireNoError(t, err)
	startBody := func(token string) string {
		return `{"token":` + strconv.Quote(token) + `,"password":` + strconv.Quote(onboardingPassword) + `}`
	}
	f.onboardingHTTP(t, onboardingStartPath, startBody(first.Token), onboardingPreauthCookie, pre, 401, nil)
	forged := pre
	forged.CSRF = strings.Repeat("A", 43)
	f.onboardingHTTP(t, onboardingStartPath, startBody(second.Token), onboardingPreauthCookie, forged, 403, nil)
	var enrollment struct {
		CSRF   string `json:"csrf_token"`
		Secret string `json:"secret"`
	}
	res := f.onboardingHTTP(t, onboardingStartPath, startBody(second.Token), onboardingPreauthCookie, pre, 200, &enrollment)
	pending := cms.SessionTokens{CSRF: enrollment.CSRF}
	for _, cookie := range res.Cookies() {
		if cookie.Name == "__Host-sp_enrollment" && cookie.Value != "" {
			pending.Token = cookie.Value
		}
	}
	if pending.Token == "" {
		t.Fatal("restricted enrollment cookie missing")
	}
	f.onboardingHTTP(t, onboardingUsersPath, `{"login":"forbidden"}`, onboardingRestrictedCookie, pending, 401, nil)
	f.onboardingHTTP(t, "/api/v1/admin/auth/step-up", `{"password":"x","code":"000000"}`, onboardingRestrictedCookie, pending, 401, nil)
	f.onboardingHTTP(t, onboardingCompletePath, `{"code":"000000","user_id":"`+f.state.user+`"}`, onboardingRestrictedCookie, pending, 400, nil)
	var completed struct {
		Codes []string `json:"recovery_codes"`
	}
	code := bootstrapCode(t, f.state.db, enrollment.Secret)
	res = f.onboardingHTTP(t, onboardingCompletePath, `{"code":"`+code+`"}`, onboardingRestrictedCookie, pending, 200, &completed)
	if len(completed.Codes) != 10 {
		t.Fatal("completion did not return ten recovery codes")
	}
	assertOnboardingCookiesCleared(t, res)
	f.onboardingHTTP(t, onboardingCompletePath, `{"code":"`+code+`"}`, onboardingRestrictedCookie, pending, 401, nil)
	var disabled, noRoles, noSessions bool
	requireNoError(t, f.state.db.QueryRow(`SELECT state='disabled' AND enrollment_completed_at IS NOT NULL, NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=u.id), NOT EXISTS(SELECT 1 FROM public.sessions WHERE user_id=u.id AND revoked_at IS NULL) FROM public.users u WHERE id=$1`, user.ID).Scan(&disabled, &noRoles, &noSessions))
	if !disabled || !noRoles || !noSessions {
		t.Fatal("enrollment activated the user or retained access")
	}
}

func assertOnboardingCookiesCleared(t *testing.T, res *http.Response) {
	t.Helper()
	for _, cookie := range res.Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatal("completion granted a session")
		}
	}
}

func TestAuthOnboardingBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newAuthHTTPFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodeExecutable, "-e", onboardingBrowserScript)
	command.Env = append(os.Environ(), "ONBOARDING_ORIGIN="+f.server.URL, "ONBOARDING_LOGIN="+bootstrapTestLogin, "ONBOARDING_PASSWORD="+bootstrapTestPassphrase, "ONBOARDING_CODE="+bootstrapCode(t, f.state.db, f.state.secret))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real onboarding Chromium scenario: %v: %s", err, output)
	}
	var disabled, noRoles bool
	requireNoError(t, f.state.db.QueryRow(`SELECT state='disabled' AND enrollment_completed_at IS NOT NULL, NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=u.id) FROM public.users u WHERE login_key='invite-browser'`).Scan(&disabled, &noRoles))
	if !disabled || !noRoles {
		t.Fatal("browser enrollment did not preserve disabled account boundary")
	}
}

const onboardingBrowserScript = `
const { chromium } = require('../../../web/node_modules/playwright');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
function totp(seed) {
 let bits=''; for(const c of seed.replace(/=+$/,'')) bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');
 const key=Buffer.from(bits.match(/.{8}/g).map(b=>parseInt(b,2)));
 const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
 const hash=crypto.createHmac('sha1',key).update(counter).digest();const offset=hash[19]&15;
 return ((hash.readUInt32BE(offset)&0x7fffffff)%1000000).toString().padStart(6,'0');
}
(async()=>{
 const browser=await chromium.launch({headless:true});
 try {
  const origin=process.env.ONBOARDING_ORIGIN;
  const issuer=await browser.newContext({ignoreHTTPSErrors:true});const admin=await issuer.newPage();
  await admin.goto(origin+'/health/live');
  async function send(page,path,body,csrf,status) {
   const result=await page.evaluate(async({path,body,csrf})=>{
    const response=await fetch(path,body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(body)});
    return {status:response.status,body:response.status===200?await response.json():null};
   },{path,body,csrf});
   assert.equal(result.status,status,'Unexpected status at '+path);return result.body;
  }
  const auth='/api/v1/admin/auth';
  const pre=await send(admin,auth+'/csrf',undefined,undefined,200);
  const login=await send(admin,auth+'/login',{login:process.env.ONBOARDING_LOGIN,password:process.env.ONBOARDING_PASSWORD},pre.csrf_token,200);
  const full=await send(admin,auth+'/mfa/verify',{code:process.env.ONBOARDING_CODE},login.csrf_token,200);
  const user=await send(admin,'/api/v1/admin/users',{login:'invite-browser'},full.csrf_token,200);
  assert.equal(user.state,'pending');
  const issue=await send(admin,'/api/v1/admin/users/'+user.id+'/onboarding-token',{expected_version:user.version},full.csrf_token,200);
  const invite=await browser.newContext({ignoreHTTPSErrors:true});const page=await invite.newPage();
  await page.goto(origin+'/health/live');
  const bootstrap=await send(page,auth+'/csrf',undefined,undefined,200);
  await send(page,auth+'/enrollment',{token:issue.token,password:'Invited account passphrase 42!'},'A'.repeat(43),403);
  const enrollment=await send(page,auth+'/enrollment',{token:issue.token,password:'Invited account passphrase 42!'},bootstrap.csrf_token,200);
  let cookies=await invite.cookies(origin);
  assert.equal(cookies.filter(c=>c.name.startsWith('__Host-sp_')).length,1,'Ambiguous enrollment cookie');
  const restricted=cookies.find(c=>c.name==='__Host-sp_enrollment');
  assert(restricted&&restricted.secure&&restricted.httpOnly&&restricted.sameSite==='Strict'&&restricted.path==='/');
  assert.equal(await page.evaluate(()=>document.cookie.includes('__Host-sp_enrollment')),false,'Enrollment cookie exposed to script');
  await send(page,'/api/v1/admin/users',{login:'forbidden'},enrollment.csrf_token,401);
  await send(page,auth+'/step-up',{password:'Invited account passphrase 42!',code:'000000'},enrollment.csrf_token,401);
  await send(page,auth+'/enrollment/complete',{code:totp(enrollment.secret)},bootstrap.csrf_token,403);
  const rotated=await send(page,auth+'/csrf',undefined,undefined,200);
  assert(rotated.csrf_token!==enrollment.csrf_token,'CSRF token did not rotate');
  assert((await invite.cookies(origin)).some(c=>c.name==='__Host-sp_enrollment'),'CSRF rotation elevated session');
  await send(page,auth+'/logout',{},enrollment.csrf_token,403);
  const complete=await send(page,auth+'/enrollment/complete',{code:totp(enrollment.secret)},rotated.csrf_token,200);
  assert.equal(complete.recovery_codes.length,10);
  assert(!(await invite.cookies(origin)).some(c=>c.name.startsWith('__Host-sp_')),'Completion retained session');
  const disabledPre=await send(page,auth+'/csrf',undefined,undefined,200);
  await send(page,auth+'/login',{login:'invite-browser',password:'Invited account passphrase 42!'},disabledPre.csrf_token,401);
  await send(page,auth+'/enrollment',{token:issue.token,password:'Invited account passphrase 42!'},disabledPre.csrf_token,401);
  const another=await send(admin,'/api/v1/admin/users',{login:'invite-browser-logout'},full.csrf_token,200);
  const anotherToken=await send(admin,'/api/v1/admin/users/'+another.id+'/onboarding-token',{expected_version:another.version},full.csrf_token,200);
  const abandoned=await send(page,auth+'/enrollment',{token:anotherToken.token,password:'Invited account passphrase 42!'},disabledPre.csrf_token,200);
  await send(page,auth+'/logout',{},disabledPre.csrf_token,403);
  await send(page,auth+'/logout',{},abandoned.csrf_token,204);
  assert(!(await invite.cookies(origin)).some(c=>c.name.startsWith('__Host-sp_')),'Restricted logout retained cookie');
 } finally {await browser.close();}
})().catch(error=>{console.error(error.stack);process.exitCode=1;});
`

func onboardingVersionJSON(t *testing.T, version int64) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		Version int64 `json:"expected_version"`
	}{version})
	requireNoError(t, err)
	return string(raw)
}
