package adminintegration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/testpostgres"
	"testing"
	"time"
)

// This browser scenario uses real security storage and the Swagger controls;
// cookies, Origin and Referer are supplied by Chromium rather than a mock.
func TestAuthSwaggerDocumentationBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newAuthHTTPFixture(t)
	itemID := seedSwaggerAssignmentItem(t, f.state)
	cfg := testpostgres.AdminConfig(t)
	requireNoError(t, f.state.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_auth"
	db := database.Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	service, err := identity.NewAuthService(db, f.state.config)
	requireNoError(t, err)
	server := newAuthTLSServer(t, service, func(auth http.Handler) http.Handler {
		return platform.HandlerWithDocumentation(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	})
	// CI reached the stale-token logout at 75 seconds. Leave room for the last
	// transitions while keeping each request independently bounded in Chromium.
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodeExecutable, "-e", qaSwaggerBrowserScript)
	command.Env = append(os.Environ(), "QA_SWAGGER_ORIGIN="+server.URL, "QA_SWAGGER_LOGIN="+bootstrapTestLogin, "QA_SWAGGER_PASSWORD="+bootstrapTestPassphrase, "QA_SWAGGER_CODE="+bootstrapCode(t, f.state.db, f.state.secret), "QA_SWAGGER_ITEM_ID="+itemID)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Swagger Chromium real-auth scenario exceeded its deadline: %s", output)
	}
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

func seedSwaggerAssignmentItem(t *testing.T, f authFixtureState) string {
	t.Helper()
	var typeID, itemID string
	requireNoError(t, f.db.QueryRow(`WITH collection AS (
    INSERT INTO public.content_types(id,key,route_namespace) VALUES(gen_random_uuid(),'article','articles') RETURNING id
), schema AS (
    INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM collection
)
INSERT INTO public.content_items(id,type_id) SELECT gen_random_uuid(),id FROM collection
RETURNING type_id::text,id::text`).Scan(&typeID, &itemID))
	_, err := f.db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope,can_delegate)
VALUES(gen_random_uuid(),$1,'content.items.assign','content',$2,'any',true)`, bootstrapAdminRoleID(t, f), typeID)
	requireNoError(t, err)
	return itemID
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
  assert.equal(await page.title(),'Portal API');
  const publicSpec=await (await context.request.get(origin+'/openapi.json')).json();
  assert(Object.keys(publicSpec.paths).every(p=>!p.includes('/admin/')),'Private route in public spec');
  assert(!JSON.stringify(publicSpec).includes('__Host-sp_'),'Private credential in public spec');
  const privateSpec=await (await context.request.get(origin+'/openapi-auth.json')).json();
  assert(Object.keys(privateSpec.paths).length>=8);
  const userRoles='/api/v1/admin/users/{id}/roles';
  const contentCollection='/api/v1/admin/content/{type_key}';
  const contentDraft=contentCollection+'/{item_id}';
  const contentAssignees=contentDraft+'/assignees';
  const contentTypes='/api/v1/admin/content-types';
  const contentMetadataPaths=[contentTypes+'/renderers',contentDraft+'/schema'];
  const managedCollection=contentTypes+'/{type_key}';
  const collectionAvailability=managedCollection+'/availability';
  const collectionCommands=['fields','icon'].map(action=>managedCollection+'/'+action);
  const settingsPath='/api/v1/admin/settings';
  const settingsPaths=[settingsPath,settingsPath+'/publication'];
  const settingsCommands=['appearance','catalogs','details','language-flags','locale-defaults','slug-rules'].map(action=>settingsPath+'/'+action);
  assert.deepEqual(Object.keys(privateSpec.paths[settingsPath]||{}),['get']);
  assert.deepEqual(Object.keys(privateSpec.paths[settingsPaths[1]]||{}),['put']);
  assert.equal(privateSpec.paths[settingsPath+'/publish'],undefined);
  const contentCommands=['ready-shared','ready','publish','unpublish','adopt-schema'].map(action=>contentDraft+'/'+action);
  for(const path of [...contentCommands,...collectionCommands,...settingsCommands]) assert.deepEqual(Object.keys(privateSpec.paths[path]||{}),['post']);
  assert.deepEqual(Object.keys(privateSpec.paths[contentCollection]||{}).sort(),['get','post']);
  for(const path of contentMetadataPaths) assert.deepEqual(Object.keys(privateSpec.paths[path]||{}),['get']);
  assert.deepEqual(Object.keys(privateSpec.paths[contentTypes]||{}).sort(),['get','post']);
  assert.deepEqual(Object.keys(privateSpec.paths[managedCollection]||{}).sort(),['get','patch']);
  assert.deepEqual(Object.keys(privateSpec.paths[collectionAvailability]||{}),['patch']);
  assert.deepEqual(Object.keys(privateSpec.paths[contentDraft]||{}).sort(),['get','patch']);
  assert.deepEqual(Object.keys(privateSpec.paths[contentAssignees]||{}).sort(),['get','put']);
  const userPaths=['/api/v1/admin/users','/api/v1/admin/users/{id}','/api/v1/admin/users/{id}/onboarding-token','/api/v1/admin/users/{id}/activate','/api/v1/admin/users/{id}/disable',userRoles];
  assert(userPaths.every(p=>privateSpec.paths[p]),'Missing private user operation');
  assert.deepEqual(Object.keys(privateSpec.paths['/api/v1/admin/users']).sort(),['get','post']);
  assert.deepEqual(Object.keys(privateSpec.paths[userRoles]).sort(),['get','put']);
  const accountReads=['/api/v1/admin/me','/api/v1/admin/sessions','/api/v1/admin/users/{id}/sessions','/api/v1/admin/audit'];
  const accountWrites=['/api/v1/admin/me/password','/api/v1/admin/me/mfa/replace','/api/v1/admin/me/recovery-codes','/api/v1/admin/sessions/{id}/revoke','/api/v1/admin/sessions/revoke-all'];
  const sessionPolicy='/api/v1/admin/security/session-policy';
  const ratePolicy='/api/v1/admin/security/rate-policy';
  assert.deepEqual(Object.keys(privateSpec.paths[sessionPolicy]||{}).sort(),['get','put']);
  assert.deepEqual(Object.keys(privateSpec.paths[ratePolicy]||{}).sort(),['get','post']);
  for(const path of accountReads) assert.deepEqual(Object.keys(privateSpec.paths[path]||{}),['get']);
  for(const path of accountWrites) assert.deepEqual(Object.keys(privateSpec.paths[path]||{}),['post']);
  const rolePaths=['/api/v1/admin/roles','/api/v1/admin/roles/{id}'];
  const roleCommandPaths=['enable','disable','archive','restore','copy','changes'].map(action=>rolePaths[1]+'/'+action);
  rolePaths.push(...roleCommandPaths,'/api/v1/admin/roles/grant-options');
  for(const path of roleCommandPaths) assert.deepEqual(Object.keys(privateSpec.paths[path]||{}),['post']);
  assert(rolePaths.every(p=>privateSpec.paths[p]),'Missing private role operation');
  assert.deepEqual(Object.keys(privateSpec.paths[rolePaths[0]]).sort(),['get','post']);
  assert.deepEqual(Object.keys(privateSpec.paths[rolePaths[1]]).sort(),['delete','get','patch']);
  const otherPaths=[contentCollection,contentDraft,contentAssignees,contentTypes,managedCollection,collectionAvailability,sessionPolicy,ratePolicy,...contentMetadataPaths,...contentCommands,...collectionCommands,...settingsPaths,...settingsCommands,...accountReads,...accountWrites];
  assert.deepEqual(Object.keys(privateSpec.paths).filter(p=>!p.startsWith('/api/v1/admin/auth/')&&!userPaths.includes(p)&&!rolePaths.includes(p)&&!otherPaths.includes(p)),[]);
  await page.locator('.topbar select').selectOption({label:'Private Administration API'});
  await page.locator('[id$="-authCSRF"]').waitFor();
  const prepared=new Set();
  async function operation(id,body,csrf,status,target) {
   console.log('Swagger operation: '+id+' expected '+status);
   const block=page.locator('[id$="-'+id+'"]');
   if(!await block.locator('.opblock-body').isVisible()) await block.locator('.opblock-summary').click();
   const tryButton=block.getByRole('button',{name:'Try it out',exact:true});
   if(!prepared.has(id)) {await tryButton.click(); prepared.add(id);}
   if(body!==undefined) await block.locator('textarea').fill(JSON.stringify(body));
   let [path,methods]=Object.entries(privateSpec.paths).find(([,v])=>Object.values(v).some(op=>op.operationId===id));
   const method=Object.keys(methods).find(key=>methods[key].operationId===id).toUpperCase();
   const params=typeof target==='string'?{id:target}:target||{};
   for(const [name,value] of Object.entries(params)) {
    await block.locator('tr[data-param-name="'+name+'"] input').fill(value);
    path=path.replace('{'+name+'}',value);
   }
   const originInput=block.locator('input[placeholder="Origin"]');
   if(body!==undefined && await originInput.count()) await originInput.fill(origin);
   const token=block.locator('input[placeholder="X-CSRF-Token"]');
   if(await token.count()) await token.fill(csrf||'');
   const [response]=await Promise.all([
    page.waitForResponse(r=>{
     const url=new URL(r.url());
     return url.origin===origin && url.pathname===path && r.request().method()===method;
    },{timeout:10000}).catch(async e=>{throw new Error(e.message+'; errors='+JSON.stringify(faults));}),
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
  let role=await operation('rolesCreate',{name:'browser-role',grants:[]},full.csrf_token,200);
  assert.equal(role.state,'disabled');
  const accounts=await operation('usersList',undefined,undefined,200);
  const admin=accounts.items.find(account=>account.login===credentials.login);
  assert(admin);
  const policies=await operation('rolesList',undefined,undefined,200);
  const seedRole=policies.items.find(policy=>policy.name==='Site Admin');
  assert(seedRole);
  const replacement={expected_version:admin.version,role_ids:[seedRole.id,role.id]};
  await operation('usersReplaceRoles',replacement,restricted.csrf_token,403,admin.id);
  const assigned=await operation('usersReplaceRoles',replacement,full.csrf_token,200,admin.id);
  assert.deepEqual(assigned,{...admin,version:admin.version+1});
  await operation('usersReplaceRoles',{expected_version:admin.version,role_ids:[seedRole.id]},full.csrf_token,409,admin.id);
  const afterAssignment=await operation('usersGet',undefined,undefined,200,admin.id);
  assert.deepEqual(afterAssignment,assigned);
  const restored=await operation('usersReplaceRoles',{expected_version:assigned.version,role_ids:[seedRole.id]},full.csrf_token,200,admin.id);
  assert.deepEqual(restored,{...admin,version:assigned.version+1});
  const item={type_key:'article',item_id:process.env.QA_SWAGGER_ITEM_ID};
  const clearAssignees={expected_version:'1',assignee_ids:[]};
  await operation('contentReplaceAssignees',clearAssignees,restricted.csrf_token,403,item);
  const assignment=await operation('contentReplaceAssignees',clearAssignees,full.csrf_token,200,item);
  assert.deepEqual(assignment,{item_id:item.item_id,version:'2',assignee_ids:[]});
  await operation('contentReplaceAssignees',clearAssignees,full.csrf_token,409,item);
  for(const [action,state] of [['Enable','active'],['Disable','disabled'],['Archive','archived'],['Restore','disabled']]){
   const expected=role.version;
   const roleID=role.id;
   role=await operation('roles'+action,{expected_version:expected},full.csrf_token,200,roleID);
   assert.deepEqual([role.id,role.state,role.version],[roleID,state,expected+1]);
  }
  const copied=await operation('rolesCopy',{name:'browser-role-copy',expected_version:role.version},full.csrf_token,200,role.id);
  assert.notEqual(copied.id,role.id);
  assert.deepEqual([copied.name,copied.state,copied.version,copied.grants],['browser-role-copy','disabled',1,[]]);
  const archived=await operation('rolesArchive',{expected_version:copied.version},full.csrf_token,200,copied.id);
  await operation('rolesDelete',{expected_version:copied.version},full.csrf_token,409,copied.id);
  await operation('rolesDelete',{expected_version:archived.version},full.csrf_token,204,copied.id);
  await operation('rolesGet',undefined,undefined,404,copied.id);
  await operation('authLogout',{},restricted.csrf_token,403);
  await operation('authLogout',{},full.csrf_token,204);
  assert(!(await context.cookies(origin)).some(c=>c.name.startsWith('__Host-sp_')));
  assert.deepEqual(faults,[]);
  console.log('PASS: local Swagger assets/rendering; live public/private separation; real HTTPS browser CSRF rejection and rotation, restricted login, MFA session rotation, versioned membership and assignee replacement, four versioned role state transitions, role copy and deletion, stale-token rejection and logout');
 } finally {await browser.close();}
})().catch(e=>{console.error(e.stack);process.exitCode=1;});
`
