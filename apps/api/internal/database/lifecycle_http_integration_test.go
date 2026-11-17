package database

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

const (
	lifecycleHTTPDisabledState = "disabled"
	lifecycleHTTPActiveState   = "active"
)

func (f authHTTPFixture) lifecycleRead(t *testing.T, path string, session cms.SessionTokens, status int) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.server.URL+path, nil)
	requireNoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Referer", f.server.URL+"/admin")
	req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: session.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	client := f.server.Client()
	client.Jar = nil
	res, err := client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != status {
		t.Fatalf("private lifecycle read status=%d expected=%d", res.StatusCode, status)
	}
	assertAuthNoStore(t, res)
	if len(res.Cookies()) != 0 {
		t.Fatal("read changed browser session")
	}
	out := map[string]any{}
	requireNoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

func TestAuthLifecycleHTTPReadAndTransitionsIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	account := lifecycleEnrolled(t, f.state, full, "lifecycle-http@example.test")
	path := onboardingUsersPath + "/" + account.user.ID
	out := f.lifecycleRead(t, path, full, 200)
	assertLifecycleSafeHTTP(t, out)
	if out["state"] != lifecycleHTTPDisabledState {
		t.Fatal("detail lost enrolled disabled state")
	}
	list := f.lifecycleRead(t, onboardingUsersPath+"?limit=1&offset=0", full, 200)
	if len(list) != 2 || len(list["items"].([]any)) != 1 || list["next_offset"] != float64(1) {
		t.Fatal("HTTP pagination lost bounded continuation")
	}
	assertLifecycleSafeHTTP(t, list["items"].([]any)[0].(map[string]any))
	tail := f.lifecycleRead(t, onboardingUsersPath+"?limit=1&offset=1", full, 200)
	if tail["next_offset"] != nil {
		t.Fatal("last page must have null continuation")
	}
	f.lifecycleRead(t, onboardingUsersPath+"/00000000-0000-4000-8000-000000000001", full, 404)
	var active, disabled onboardingUserHTTP
	f.onboardingHTTP(t, path+"/activate", onboardingVersionJSON(t, account.user.Version-1), onboardingIssuerCookie, full, 409, nil)
	f.onboardingHTTP(t, path+"/activate", onboardingVersionJSON(t, account.user.Version), onboardingIssuerCookie, full, 200, &active)
	if active.State != lifecycleHTTPActiveState || active.Version != account.user.Version+1 {
		t.Fatal("activation failed state/version contract")
	}
	account.user.Version, account.user.State = active.Version, active.State
	target := lifecycleLoginFull(t, f.state, account)
	f.lifecycleRead(t, path, target, 403)
	f.onboardingHTTP(t, path+"/disable", onboardingVersionJSON(t, active.Version), onboardingIssuerCookie, full, 200, &disabled)
	if disabled.State != lifecycleHTTPDisabledState || disabled.Version != active.Version+1 {
		t.Fatal("disable failed state/version contract")
	}
	f.lifecycleRead(t, path, target, 401)
	var noMembership, audited bool
	requireNoError(t, f.state.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=$1),(SELECT count(*)=2 FROM public.security_audit WHERE target_id=$1 AND operation IN ('users.activated','users.disabled') AND result='success')`, account.user.ID).Scan(&noMembership, &audited))
	if !noMembership || !audited {
		t.Fatal("lifecycle assigned access or missed audit")
	}
}

func assertLifecycleSafeHTTP(t *testing.T, out map[string]any) {
	t.Helper()
	if len(out) != 4 || out["id"] == nil || out["login"] == nil || out["state"] == nil || out["version"] == nil {
		t.Fatal("HTTP read escaped safe account projection")
	}
}

func TestAuthLifecycleHTTPFreshnessAndLastAdminIntegration(t *testing.T) {
	f := newAuthHTTPFixture(t)
	full := authFull(t, f.state)
	account := lifecycleEnrolled(t, f.state, full, "lifecycle-freshness@example.test")
	_, err := f.state.db.Exec(`UPDATE public.sessions SET step_up_at=NULL WHERE token_digest=$1`, authDigest(full.Token))
	requireNoError(t, err)
	f.lifecycleRead(t, onboardingUsersPath, full, 200)
	f.onboardingHTTP(t, onboardingUsersPath+"/"+account.user.ID+"/activate", onboardingVersionJSON(t, account.user.Version), onboardingIssuerCookie, full, 403, nil)
	_, err = f.state.db.Exec(`UPDATE public.sessions SET step_up_at=last_seen WHERE token_digest=$1`, authDigest(full.Token))
	requireNoError(t, err)
	var version int64
	requireNoError(t, f.state.db.QueryRow(`SELECT version FROM public.users WHERE id=$1`, f.state.user).Scan(&version))
	var problem struct {
		Code string `json:"code"`
	}
	f.onboardingHTTP(t, onboardingUsersPath+"/"+f.state.user+"/disable", onboardingVersionJSON(t, version), onboardingIssuerCookie, full, 409, &problem)
	if problem.Code != "last_admin_required" {
		t.Fatal("last-admin refusal lost machine code")
	}

	f.lifecycleRead(t, onboardingUsersPath, full, 200)
}

func TestAuthLifecycleBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newAuthHTTPFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodeExecutable, "-e", lifecycleBrowserScript)
	command.Env = append(os.Environ(), "LIFECYCLE_ORIGIN="+f.server.URL, "LIFECYCLE_LOGIN="+bootstrapTestLogin, "LIFECYCLE_PASSWORD="+bootstrapTestPassphrase, "LIFECYCLE_CODE="+bootstrapCode(t, f.state.db, f.state.secret))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real lifecycle Chromium scenario: %v: %s", err, output)
	}
}

const lifecycleBrowserScript = `
const {chromium}=require('../../../web/node_modules/playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({headless:true});
 try {
  const origin=process.env.LIFECYCLE_ORIGIN;
  const context=await browser.newContext({ignoreHTTPSErrors:true});
  const page=await context.newPage();await page.goto(origin+'/health/live');
  async function send(path,body,csrf,status) {
   const result=await page.evaluate(async({path,body,csrf})=>{
    const response=await fetch(path,body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json',...(csrf?{'X-CSRF-Token':csrf}:{})},body:JSON.stringify(body)});
    return {status:response.status,body:await response.json(),cache:response.headers.get('Cache-Control')};
   },{path,body,csrf});
   assert.equal(result.status,status,'Unexpected status at '+path);assert.equal(result.cache,'no-store');return result.body;
  }
  const auth='/api/v1/admin/auth',users='/api/v1/admin/users';
  const pre=await send(auth+'/csrf',undefined,undefined,200);
  const login=await send(auth+'/login',{login:process.env.LIFECYCLE_LOGIN,password:process.env.LIFECYCLE_PASSWORD},pre.csrf_token,200);
  const full=await send(auth+'/mfa/verify',{code:process.env.LIFECYCLE_CODE},login.csrf_token,200);
  const cookies=await context.cookies(origin);const cookie=cookies.find(c=>c.name==='__Host-sp_session');
  assert(cookie&&cookie.secure&&cookie.httpOnly&&cookie.sameSite==='Strict'&&cookie.path==='/');
  assert.equal(await page.evaluate(()=>document.cookie.includes('__Host-sp_session')),false);
  const list=await send(users+'?limit=1',undefined,undefined,200);
  assert.deepEqual(Object.keys(list).sort(),['items','next_offset']);assert.equal(list.items.length,1);
  const user=list.items[0];assert.deepEqual(Object.keys(user).sort(),['id','login','state','version']);
  const detail=await send(users+'/'+user.id,undefined,undefined,200);assert.equal(detail.id,user.id);
  await send(users+'/'+user.id+'/disable',{expected_version:user.version},undefined,403);
  const last=await send(users+'/'+user.id+'/disable',{expected_version:user.version},full.csrf_token,409);assert.equal(last.code,'last_admin_required');
  await send(users,undefined,undefined,200);
 } finally {await browser.close();}
})().catch(error=>{console.error(error.stack);process.exitCode=1;});
`
