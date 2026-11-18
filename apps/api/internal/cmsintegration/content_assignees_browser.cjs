const assert = require('node:assert/strict');
const { browserAdminClient, chromium } = require('./browser_admin_client.cjs');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const origin = process.env.ASSIGN_ORIGIN;
  const item = process.env.ASSIGN_ITEM;
  const type = process.env.ASSIGN_TYPE;
  const recipient = process.env.ASSIGN_RECIPIENT;
  const missing = process.env.ASSIGN_UNKNOWN;
  const replacement = { assignee_ids: [recipient], expected_version: '1' };
  const route = (key, id) => `/api/v1/admin/content/${key}/${id}/assignees`;
  const path = route(type, item);

  const { open, send, login } = browserAdminClient(browser, origin, 'ASSIGN');

  function put(page, status, body, csrf, target = path) {
    return send(page, target, status, { method: 'PUT', body, csrf });
  }

  try {
    const manager = await open();
    if (process.env.ASSIGN_MODE === 'session') {
      assert.equal((await put(manager, 401, replacement)).body.code, 'authentication_required');
      const csrf = await login(manager, 'MANAGER', async restricted => {
        assert.equal((await put(manager, 401, replacement, restricted)).body.code, 'authentication_required');
      });

      const stale = await open();
      await stale.context().addCookies([{
        name: '__Host-sp_session', value: process.env.ASSIGN_STALE_TOKEN, url: origin,
        secure: true, httpOnly: true, sameSite: 'Strict',
      }]);
      assert.equal((await put(stale, 403, replacement, process.env.ASSIGN_STALE_CSRF)).body.code, 'step_up_required');
      assert.equal((await put(manager, 403, replacement)).body.code, 'csrf_failed');

      const reader = await open();
      const readerCSRF = await login(reader, 'RECIPIENT');
      assert.equal((await put(reader, 403, replacement, readerCSRF)).body.code, 'permission_denied');
      assert.equal((await put(manager, 403, { ...replacement, assignee_ids: [process.env.ASSIGN_MANAGER] }, csrf)).body.code, 'permission_denied');
      for (const assignee_ids of [undefined, null, ['invalid-id']]) {
        assert.equal((await put(manager, 400, { ...replacement, assignee_ids }, csrf)).body.code, 'validation_failed');
      }
      return;
    }

    const csrf = await login(manager, 'MANAGER');
    for (const expected_version of [1, '01', '0', '9223372036854775808']) {
      assert.equal((await put(manager, 400, { ...replacement, expected_version }, csrf)).body.code, 'validation_failed');
    }
    const absent = await put(manager, 404, replacement, csrf, route(type, missing));
    const scope = await put(manager, 404, replacement, csrf, route(process.env.ASSIGN_OTHER_TYPE, process.env.ASSIGN_OTHER_ITEM));
    const wrongType = await put(manager, 404, replacement, csrf, route(type, process.env.ASSIGN_OTHER_ITEM));
    assert.equal(absent.body.code, 'not_found');
    assert.deepEqual(scope.body, absent.body);
    assert.deepEqual(wrongType.body, absent.body);
    const inactive = await put(manager, 409, { ...replacement, assignee_ids: [process.env.ASSIGN_INACTIVE] }, csrf);
    assert.equal(inactive.body.code, 'invalid_transition');

    const updated = await put(manager, 200, replacement, csrf);
    assert.deepEqual(updated.body, { item_id: item, version: '2', assignee_ids: [recipient] });
    assert.equal((await put(manager, 409, replacement, csrf)).body.code, 'version_conflict');
    const cleared = await put(manager, 200, { expected_version: '2', assignee_ids: [] }, csrf);
    assert.deepEqual(cleared.body, { item_id: item, version: '3', assignee_ids: [] });
    for (const id of [updated.requestID, cleared.requestID]) {
      assert.match(id, /^[0-9a-f-]{36}$/);
    }
    assert.notEqual(updated.requestID, cleared.requestID);
    process.stdout.write(JSON.stringify([updated.requestID, cleared.requestID]));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
