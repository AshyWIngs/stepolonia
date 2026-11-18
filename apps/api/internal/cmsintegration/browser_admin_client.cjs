const assert = require('node:assert/strict');

function browserAdminClient(browser, origin, variablePrefix) {
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
    assert.equal(result.status, status, method + ' ' + path + ' ' + result.body.code);
    assert.equal(result.cache, 'no-store');
    return result;
  }

  async function login(page, who, beforeMFA) {
    const auth = '/api/v1/admin/auth';
    const account = variablePrefix + '_' + who;
    const preauth = (await send(page, auth + '/csrf', 200)).body;
    const pending = (await send(page, auth + '/login', 200, {
      method: 'POST', csrf: preauth.csrf_token,
      body: { login: process.env[account + '_LOGIN'], password: process.env[variablePrefix + '_PASSWORD'] },
    })).body;
    if (beforeMFA) await beforeMFA(pending.csrf_token);
    return (await send(page, auth + '/mfa/verify', 200, {
      method: 'POST', csrf: pending.csrf_token, body: { code: process.env[account + '_CODE'] },
    })).body.csrf_token;
  }

  return { open, send, login };
}

module.exports = { browserAdminClient };
