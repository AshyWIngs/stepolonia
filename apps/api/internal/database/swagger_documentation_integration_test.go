package database

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/cmshttp"
	"stepolonia/apps/api/internal/platform"
)

// This browser scenario uses real security storage and the Swagger controls;
// cookies, Origin and Referer are supplied by Chromium rather than a mock.
func TestAuthSwaggerDocumentationBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newAuthHTTPFixture(t)
	cfg := cmsDisposableDatabaseConfig(t)
	requireNoError(t, f.state.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_auth"
	db := Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	service, err := cms.NewAuthService(db, f.state.config)
	requireNoError(t, err)
	var handler http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer server.Close()
	auth, err := cmshttp.NewHandler(cmshttp.Config{CanonicalOrigin: server.URL}, service)
	requireNoError(t, err)
	handler = platform.HandlerWithDocumentation(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodeExecutable, "-e", qaSwaggerBrowserScript)
	command.Env = append(os.Environ(), "QA_SWAGGER_ORIGIN="+server.URL, "QA_SWAGGER_LOGIN="+bootstrapTestLogin, "QA_SWAGGER_PASSWORD="+bootstrapTestPassphrase, "QA_SWAGGER_CODE="+bootstrapCode(t, f.state.db, f.state.secret))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Swagger Chromium real-auth scenario: %v: %s", err, output)
	}
	var active, revoked int
	requireNoError(t, f.state.db.QueryRow(`SELECT count(*) FILTER (WHERE revoked_at IS NULL), count(*) FILTER (WHERE kind='full' AND revoked_at IS NOT NULL) FROM public.sessions`).Scan(&active, &revoked))
	if active != 0 || revoked != 1 {
		t.Fatalf("Swagger logout storage state: active=%d revoked full=%d", active, revoked)
	}
	t.Log(string(output))
}

const qaSwaggerBrowserScript = `
const { chromium } = require('../../../web/node_modules/playwright');
const assert = require('node:assert/strict');
(async () => {
 const browser=await chromium.launch({headless:true});
 try {
  const origin=process.env.QA_SWAGGER_ORIGIN;
  const context=await browser.newContext({ignoreHTTPSErrors:true});
  const page=await context.newPage();
  const faults=[];
  page.on('pageerror',e=>faults.push(e.message));
  page.on('request',r=>{if(r.url().includes('/auth/')) console.log('Request '+r.method()+' '+new URL(r.url()).pathname);});
  page.on('requestfailed',r=>console.log('Request failed '+new URL(r.url()).pathname+' '+r.failure()?.errorText));
  page.on('response',r=>{if(r.url().includes('/auth/')) console.log('Response '+r.status()+' '+new URL(r.url()).pathname);});
  page.on('request',r=>{if(new URL(r.url()).origin!==origin) faults.push('External dependency requested');});
  await page.goto(origin+'/docs');
  await page.locator('.opblock').first().waitFor().catch(async e=>{throw new Error(e.message+'; browser errors: '+JSON.stringify(faults)+'; rendered: '+await page.locator('body').innerText());});
  assert.equal(await page.title(),'Stepolonia API');
  const publicSpec=await (await context.request.get(origin+'/openapi.json')).json();
  assert(Object.keys(publicSpec.paths).every(p=>!p.includes('/admin/')),'Private route in public spec');
  assert(!JSON.stringify(publicSpec).includes('__Host-sp_'),'Private credential in public spec');
  const privateSpec=await (await context.request.get(origin+'/openapi-auth.json')).json();
  assert(Object.keys(privateSpec.paths).length>=8);
  const userPaths=['/api/v1/admin/users','/api/v1/admin/users/{id}/onboarding-token'];
  assert(userPaths.every(p=>privateSpec.paths[p]),'Missing private onboarding issuer operation');
  assert(Object.keys(privateSpec.paths).every(p=>p.startsWith('/api/v1/admin/auth/')||userPaths.includes(p)));
  await page.locator('.topbar select').selectOption({label:'Private authentication'});
  await page.locator('[id$="-authCSRF"]').waitFor();
  const prepared=new Set();
  async function operation(id,body,csrf,status) {
   console.log('Swagger operation: '+id+' expected '+status);
   const block=page.locator('[id$="-'+id+'"]');
   if(!await block.locator('.opblock-body').isVisible()) await block.locator('.opblock-summary').click();
   const tryButton=block.getByRole('button',{name:'Try it out',exact:true});
   if(!prepared.has(id)) {await tryButton.click(); prepared.add(id);}
   if(body!==undefined) await block.locator('textarea').fill(JSON.stringify(body));
   const originInput=block.locator('input[placeholder="Origin"]');
   if(body!==undefined && await originInput.count()) await originInput.fill(origin);
   const token=block.locator('input[placeholder="X-CSRF-Token"]');
   if(await token.count()) await token.fill(csrf||'');
   const [response]=await Promise.all([
    page.waitForResponse(r=>r.url()===origin+Object.entries(privateSpec.paths).find(([p,v])=>Object.values(v).some(op=>op.operationId===id))[0] && r.request().method()!=='OPTIONS',{timeout:10000}).catch(async e=>{throw new Error(e.message+'; errors='+JSON.stringify(faults));}),
    block.getByRole('button',{name:'Execute',exact:true}).click()
   ]);
   assert.equal(response.status(),status,id+' unexpected HTTP status');
   await block.locator('.live-responses-table').waitFor();
   return status===200?await response.json():null;
  }
  const pre=await operation('authCSRF',undefined,undefined,200);
  assert.equal(pre.csrf_token.length,43);
  const credentials={login:process.env.QA_SWAGGER_LOGIN,password:process.env.QA_SWAGGER_PASSWORD};
  await operation('authLogin',credentials,'A'.repeat(43),403);
  const restricted=await operation('authLogin',credentials,pre.csrf_token,200);
  assert.notEqual(restricted.csrf_token,pre.csrf_token);
  let cookies=await context.cookies(origin);
  assert(cookies.some(c=>c.name==='__Host-sp_login'&&c.httpOnly&&c.secure&&c.sameSite==='Strict'));
  assert(!cookies.some(c=>c.name==='__Host-sp_session'));
  await operation('authVerifyMFA',{code:process.env.QA_SWAGGER_CODE},pre.csrf_token,403);
  const full=await operation('authVerifyMFA',{code:process.env.QA_SWAGGER_CODE},restricted.csrf_token,200);
  assert.notEqual(full.csrf_token,restricted.csrf_token);
  cookies=await context.cookies(origin);
  assert(cookies.some(c=>c.name==='__Host-sp_session'&&c.httpOnly&&c.secure&&c.sameSite==='Strict'));
  assert(!cookies.some(c=>c.name==='__Host-sp_login'));
  await operation('authLogout',{},restricted.csrf_token,403);
  await operation('authLogout',{},full.csrf_token,204);
  assert(!(await context.cookies(origin)).some(c=>c.name.startsWith('__Host-sp_')));
  assert.deepEqual(faults,[]);
  console.log('PASS: local Swagger assets/rendering; live public/private separation; real HTTPS browser CSRF rejection and rotation, restricted login, MFA session rotation, stale-token rejection and logout');
 } finally {await browser.close();}
})().catch(e=>{console.error(e.stack);process.exitCode=1;});
`
