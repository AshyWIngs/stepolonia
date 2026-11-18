package cmsintegration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestAuthUserRolesBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newRestrictedAuthHTTPFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin := authFull(t, f.state)
	recipient := lifecycleEnrolled(t, f.state, admin, "membership-browser@example.test")
	user, err := f.state.service.ActivateUser(ctx, authReq(admin), recipient.user.ID, recipient.user.Version)
	requireNoError(t, err)
	oldRole := createAssignedTestRole(t, f.state, "Browser role reader", lifecycleHTTPActiveState, user.ID)
	userReader := createMembershipTestRole(t, f.state, "Browser user reader", lifecycleHTTPActiveState)
	inactiveReader := createMembershipTestRole(t, f.state, "Inactive role reader", lifecycleHTTPDisabledState)
	const readRoles = "roles.read"
	addTestInstallationGrant(t, f.state, oldRole, readRoles)
	addTestInstallationGrant(t, f.state, inactiveReader, readRoles)
	addTestInstallationGrant(t, f.state, userReader, userReaderGrant().Operation)
	_, err = f.state.db.Exec(`UPDATE public.mfa_totp SET last_counter=last_counter-1 WHERE user_id IN ($1,$2)`, f.state.user, user.ID)
	requireNoError(t, err)

	command := exec.CommandContext(ctx, nodeExecutable, "-e", userRolesBrowserScript)
	command.Env = append(os.Environ(),
		"MEMBERSHIP_ORIGIN="+f.server.URL, "MEMBERSHIP_PASSWORD="+bootstrapTestPassphrase,
		"MEMBERSHIP_ADMIN_LOGIN="+bootstrapTestLogin, "MEMBERSHIP_ADMIN_CODE="+bootstrapCode(t, f.state.db, f.state.secret),
		"MEMBERSHIP_RECIPIENT_LOGIN="+user.Login, "MEMBERSHIP_RECIPIENT_CODE="+bootstrapCode(t, f.state.db, recipient.secret),
		"MEMBERSHIP_RECIPIENT_ID="+user.ID, "MEMBERSHIP_VERSION="+strconv.FormatInt(user.Version, 10),
		"MEMBERSHIP_OLD_ROLE="+oldRole, "MEMBERSHIP_ACTIVE_ROLE="+userReader, "MEMBERSHIP_INACTIVE_ROLE="+inactiveReader,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real membership Chromium scenario: %v: %s", err, output)
	}
	var requestIDs [2]string
	requireNoError(t, json.Unmarshal(output, &requestIDs))
	assertMembershipState(t, f.state, user, user.Version+2, nil)
	var audited bool
	requireNoError(t, f.state.db.QueryRow(`
 SELECT count(*)=2 AND bool_and(actor_id=$1 AND request_id IN ($3,$4))
 FROM public.security_audit WHERE target_id=$2 AND operation='users.roles_replaced' AND result='success'`,
		f.state.user, user.ID, requestIDs[0], requestIDs[1]).Scan(&audited))
	if !audited {
		t.Fatal("membership replacement lost success audit correlation")
	}
}

const userRolesBrowserScript = `
const { chromium } = require('../../../web/node_modules/playwright');
const assert = require('node:assert/strict');

(async () => {
  const origin = process.env.MEMBERSHIP_ORIGIN;
  const browser = await chromium.launch({ headless: true });
  try {
    const users = '/api/v1/admin/users';
    const roles = '/api/v1/admin/roles';
    const auth = '/api/v1/admin/auth';
    const user = process.env.MEMBERSHIP_RECIPIENT_ID;
    const path = users + '/' + user + '/roles';
    const version = Number(process.env.MEMBERSHIP_VERSION);
    const oldRole = process.env.MEMBERSHIP_OLD_ROLE;
    const newRoles = [process.env.MEMBERSHIP_ACTIVE_ROLE, process.env.MEMBERSHIP_INACTIVE_ROLE];
    const replacement = { role_ids: newRoles, expected_version: version };

    async function open() {
      const context = await browser.newContext({ ignoreHTTPSErrors: true });
      const page = await context.newPage();
      await page.goto(origin + '/health/live');
      return page;
    }
    async function send(page, path, status, { method = 'GET', body, csrf } = {}) {
      const result = await page.evaluate(async ({ path, method, body, csrf }) => {
        const response = await fetch(path, {
          method,
          headers: body === undefined ? {} : {
            'Content-Type': 'application/json',
            ...(csrf ? { 'X-CSRF-Token': csrf, 'X-Request-ID': 'forged-client-id' } : {}),
          },
          body: JSON.stringify(body),
        });
        return {
          status: response.status, body: await response.json(),
          cache: response.headers.get('Cache-Control'), requestID: response.headers.get('X-Request-ID'),
        };
      }, { path, method, body, csrf });
      assert.equal(result.status, status, method + ' ' + path);
      assert.equal(result.cache, 'no-store');
      return result;
    }
    async function login(page, who, beforeMFA) {
      const preauth = (await send(page, auth + '/csrf', 200)).body;
      const pending = (await send(page, auth + '/login', 200, {
        method: 'POST', csrf: preauth.csrf_token,
        body: { login: process.env['MEMBERSHIP_' + who + '_LOGIN'], password: process.env.MEMBERSHIP_PASSWORD },
      })).body;
      if (beforeMFA) await beforeMFA(pending.csrf_token);
      return (await send(page, auth + '/mfa/verify', 200, {
        method: 'POST', csrf: pending.csrf_token, body: { code: process.env['MEMBERSHIP_' + who + '_CODE'] },
      })).body.csrf_token;
    }

    const operator = await open();
    const recipient = await open();
    const put = (status, body, csrf) => send(operator, path, status, { method: 'PUT', body, csrf });
    const csrf = await login(operator, 'ADMIN', async restricted => {
      const response = await put(401, replacement, restricted);
      assert.equal(response.body.code, 'authentication_required');
    });
    const recipientCSRF = await login(recipient, 'RECIPIENT');
    await send(recipient, roles + '/' + oldRole, 200);
    await send(recipient, users, 403);
    assert.equal((await send(recipient, path, 403, { method: 'PUT', body: replacement, csrf: recipientCSRF })).body.code, 'permission_denied');

    assert.equal((await put(403, replacement)).body.code, 'csrf_failed');
    for (const role_ids of [undefined, null, ['invalid-id']]) {
      assert.equal((await put(400, { role_ids, expected_version: version }, csrf)).body.code, 'validation_failed');
    }
    assert.equal((await put(404, { role_ids: ['00000000-0000-4000-8000-000000000099'], expected_version: version }, csrf)).body.code, 'not_found');
    const replaced = await put(200, replacement, csrf);
    assert.deepEqual(replaced.body, {
      id: user, login: process.env.MEMBERSHIP_RECIPIENT_LOGIN, state: 'active', version: version + 1,
    });
    await send(recipient, roles + '/' + oldRole, 403);
    await send(recipient, users, 200);
    assert.equal((await put(409, replacement, csrf)).body.code, 'version_conflict');
    const cleared = await put(200, { role_ids: [], expected_version: version + 1 }, csrf);
    assert.deepEqual(cleared.body, { ...replaced.body, version: version + 2 });
    await send(recipient, users, 403);
    for (const id of [replaced.requestID, cleared.requestID]) {
      assert.match(id, /^[0-9a-f-]{36}$/);
    }
    assert.notEqual(replaced.requestID, cleared.requestID);
    process.stdout.write(JSON.stringify([replaced.requestID, cleared.requestID]));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
`
