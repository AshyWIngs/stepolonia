import assert from 'node:assert/strict'

export async function signInAfterAnotherTabRefreshesPreauth(page, signIn) {
  const endpoint = new URL(page.url())
  assert.equal(endpoint.protocol, 'https:')
  assert.equal(endpoint.hostname, '127.0.0.1')
  assert.equal(endpoint.pathname, '/admin')

  const csrf = new URL('/api/v1/admin/auth/csrf', endpoint.origin).href
  const login = new URL('/api/v1/admin/auth/login', endpoint.origin).href
  const otherTab = await page.context().newPage()
  try {
    const [response] = await Promise.all([
      otherTab.waitForResponse(candidate => candidate.url() === csrf),
      otherTab.goto(endpoint.href),
    ])
    assert.equal(response.status(), 200)
    assert.equal((await response.json()).session_kind, 'preauth')
    await otherTab.getByRole('heading', { name: 'Sign in', exact: true }).waitFor()
  }
  finally {
    await otherTab.close()
  }

  let loginPosts = 0
  function countLogin(request) {
    if (request.url() === login && request.method() === 'POST') loginPosts++
  }
  page.on('request', countLogin)
  try {
    await signIn()
    assert.equal(loginPosts, 1, 'The original tab must submit the password once after refreshing preauth')
  }
  finally {
    page.off('request', countLogin)
  }
}
