const assert = require('node:assert/strict');
const { browserAdminClient, chromium } = require('./browser_admin_client.cjs');

(async () => {
  const origin = process.env.MEMBERSHIP_ORIGIN;
  const browser = await chromium.launch({ headless: true });
  try {
    const users = '/api/v1/admin/users';
    const roles = '/api/v1/admin/roles';
    const user = process.env.MEMBERSHIP_RECIPIENT_ID;
    const path = users + '/' + user + '/roles';
    const version = Number(process.env.MEMBERSHIP_VERSION);
    const oldRole = process.env.MEMBERSHIP_OLD_ROLE;
    const newRoles = [process.env.MEMBERSHIP_ACTIVE_ROLE, process.env.MEMBERSHIP_INACTIVE_ROLE];
    const replacement = { role_ids: newRoles, expected_version: version };

    const { open, send, login } = browserAdminClient(browser, origin, 'MEMBERSHIP');

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
