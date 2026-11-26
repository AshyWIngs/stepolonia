import assert from 'node:assert/strict'
import process from 'node:process'
import { chromium } from 'playwright'
import { verifyAccessAdministration, waitForContentMutation } from './admin-access.mjs'
import { admin, assertFit, assertTheme, heading, origin, verifyScopedSaveButton } from './admin-browser-helpers.mjs'
import { verifyContentWorkflow, verifyEditorialNavigation } from './admin-editorial.mjs'
import { assertPublishedLocales, verifyCompactNavigation, verifyNestedNavigation, verifyWorkspaceGeometry } from './admin-layout.mjs'
import { signInAfterAnotherTabRefreshesPreauth } from './admin-preauth.mjs'
import { verifyCustomCollection, verifyTypeBuilder } from './admin-registry.mjs'
import { verifyCatalogConflictAfterDirectorySave, verifyLanguageFlagSettings, verifySettingsSave, verifySlugRuleSettings, verifyTextSearchAfterResize } from './admin-settings.mjs'
import { chooseTheme } from './header-preferences.mjs'

const authPath = '/api/v1/admin/auth/'
const sessionCookie = '__Host-sp_session'

function waitForAuth(page, action, status) {
  return page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.origin === origin && url.pathname === authPath + action && response.status() === status
  })
}

async function assertLayouts(page) {
  const verifyViewport = async (width, height) => {
    await page.setViewportSize({ width, height })
    const verifyTheme = async value => {
      await chooseTheme(page, value)
      await assertTheme(page, value, value)
      await assertFit(page)
      assert.ok(await page.getByRole('button', { name: 'Continue', exact: true }).isVisible())
      console.log(`Administration sign-in visible at ${width}×${height} in ${value} mode`)
    }
    await verifyTheme('light')
    await verifyTheme('dark')
  }
  await verifyViewport(1440, 900)
  await verifyViewport(768, 1024)
  await verifyViewport(390, 844)
  await page.setViewportSize({ width: 320, height: 844 })
  await assertFit(page)
  await page.reload()
  await heading(page, 'Sign in').waitFor()
  await assertTheme(page, 'dark', 'dark')
  await chooseTheme(page, 'system')
  await page.emulateMedia({ colorScheme: 'dark' })
  await assertTheme(page, 'system', 'dark')
  await page.emulateMedia({ colorScheme: 'light' })
  await assertTheme(page, 'system', 'light')
}

async function signIn(page, context) {
  const loginField = page.getByLabel('Login', { exact: true })
  const password = page.getByLabel('Password', { exact: true })
  await loginField.fill('editor')
  assert.ok(await loginField.evaluate(element => element.validity.valid))
  await loginField.clear()
  await loginField.focus()
  await page.keyboard.type(process.env.QA_ADMIN_LOGIN)
  await page.keyboard.press('Tab')
  assert.ok(await password.evaluate(element => element === document.activeElement))
  await password.fill('Incorrect test password')
  const denied = waitForAuth(page, 'login', 401)
  await password.press('Enter')
  await denied
  await page.getByRole('alert').waitFor()
  await heading(page, 'Sign in').waitFor()
  assert.equal(await password.inputValue(), '')
  await loginField.evaluate((element, credentials) => {
    element.value = credentials.login
    element.form.elements.namedItem('password').value = credentials.password
  }, { login: process.env.QA_ADMIN_LOGIN, password: process.env.QA_ADMIN_PASSWORD })
  await signInAfterAnotherTabRefreshesPreauth(page, async () => {
    const login = waitForAuth(page, 'login', 200)
    await password.press('Enter')
    await login
    await heading(page, 'Two-step verification').waitFor()
  })
  await page.waitForFunction(() => document.activeElement === document.querySelector('main h1'))
  assert.ok(!(await context.cookies(origin)).some(cookie => cookie.name === sessionCookie))

  const challenge = waitForAuth(page, 'csrf', 200)
  await page.reload()
  assert.equal((await (await challenge).json()).session_kind, 'login')
  await heading(page, 'Two-step verification').waitFor()
  await page.setViewportSize({ width: 390, height: 844 })
  await assertFit(page)
  const code = page.getByLabel('Verification code', { exact: true })
  assert.equal(await code.getAttribute('autocomplete'), 'one-time-code')
  await code.fill(process.env.QA_ADMIN_CODE)
  const verify = waitForAuth(page, 'mfa/verify', 200)
  await code.press('Enter')
  await verify
  await heading(page, 'Signed in').waitFor()
  const cookie = (await context.cookies(origin)).find(item => item.name === sessionCookie)
  assert.ok(cookie?.httpOnly && cookie.secure && cookie.sameSite === 'Strict')
  const refresh = waitForAuth(page, 'csrf', 200)
  await page.reload()
  assert.equal((await (await refresh).json()).session_kind, 'full')
  await heading(page, 'Signed in').waitFor()
  await page.setViewportSize({ width: 768, height: 1024 })
  await assertFit(page)
  return cookie
}

async function signOut(page, context, revokedCookie) {
  const peer = await context.newPage()
  await peer.goto(admin)
  await peer.locator('.admin-navigation').waitFor()
  // Session restoration rotates CSRF. Refresh the tab that performs logout.
  await page.reload()
  const logout = waitForAuth(page, 'logout', 204)
  await page.getByRole('button', { name: 'Sign out', exact: true }).click()
  await logout
  await heading(page, 'Sign in').waitFor()
  assert.ok(!(await context.cookies(origin)).some(cookie => cookie.name === sessionCookie))

  await peer.evaluate(() => window.dispatchEvent(new Event('focus')))
  await heading(peer, 'Sign in').waitFor()
  assert.equal(await peer.locator('.admin-navigation').count(), 0)
  await peer.close()

  // Simulate a browser that still has a previously valid cookie after revocation.
  await context.clearCookies()
  await context.addCookies([revokedCookie])
  const rejected = waitForAuth(page, 'csrf', 401)
  await page.reload()
  await rejected
  await heading(page, 'Sign in').waitFor()
  assert.ok(!(await context.cookies(origin)).some(cookie => cookie.name === sessionCookie))
}

async function verifySessionPolicy(page) {
  const action = name => page.getByRole('button', { name, exact: true })
  await page.locator('header').getByRole('button', { name: 'My account', exact: true }).click()
  await heading(page, 'Active sessions').waitFor()
  await action('Access').click()
  await action('Security').click()
  const idle = page.getByLabel('Inactivity limit (minutes)', { exact: true })
  const absolute = page.getByLabel('Maximum session length (hours)', { exact: true })
  assert.equal(await idle.inputValue(), '30')
  assert.equal(await absolute.inputValue(), '12')
  await idle.fill('45')
  await absolute.fill('8')
  const saved = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/admin/security/session-policy' &&
    response.request().method() === 'PUT' && response.status() === 200)
  await action('Save session policy').click()
  const policy = await (await saved).json()
  assert.equal(policy.idle_timeout_minutes, 45)
  assert.equal(policy.absolute_timeout_hours, 8)
  await page.getByText('Session policy saved.', { exact: true }).waitFor()
}

async function verifyRatePolicy(page) {
  const section = page.getByRole('button', { name: 'Rate limits', exact: true })
  await section.click()
  await page.waitForURL(url => url.searchParams.get('section') === 'access' && url.searchParams.get('view') === 'rate_limits')
  await page.reload()
  const credentials = page.locator('form[data-rate-scope="credentials"]')
  const preauth = page.locator('form[data-rate-scope="preauth"]')
  const activeWindow = credentials.locator('input[name="window_seconds"]')
  const otherWindow = preauth.locator('input[name="window_seconds"]')
  const save = credentials.locator('button[type="submit"]')
  await verifyScopedSaveButton(save, 'input[name="window_seconds"]')
  assert.equal(await section.getAttribute('aria-current'), 'page')
  const original = await activeWindow.inputValue()
  const other = await otherWindow.inputValue()
  await otherWindow.fill(String(Number(other) + 1))
  await activeWindow.fill(String(Number(original) + 1))
  const saveBeforeHover = await save.evaluate(button => getComputedStyle(button).boxShadow)
  await save.hover()
  await page.waitForFunction((before) => {
    const button = document.querySelector('form[data-rate-scope="credentials"] button[type="submit"]')
    return button && getComputedStyle(button).boxShadow !== before
  }, saveBeforeHover)
  const path = '/api/v1/admin/security/rate-policy'
  const saved = waitForContentMutation(page, path, 'POST', 200)
  await save.click()
  const updated = await (await saved).json()
  assert.equal(updated.scope, 'credentials')
  assert.equal(updated.window_seconds, Number(original) + 1)
  assert.equal(await otherWindow.inputValue(), String(Number(other) + 1))
  await otherWindow.fill(other)
  await activeWindow.fill(original)
  const restored = waitForContentMutation(page, path, 'POST', 200)
  await save.click()
  assert.equal((await (await restored).json()).window_seconds, Number(original))
}

async function verifyPermissionTextNavigation(page) {
  await page.getByRole('button', { name: 'Roles', exact: true }).click()
  await page.getByRole('button', { name: 'Translate permission labels', exact: true }).click()
  await page.waitForURL(url => url.searchParams.get('view') === 'translations' && url.searchParams.get('group') === 'permissions')
  await page.reload()
  const group = page.locator('#portal-message-group')
  await group.waitFor()
  assert.equal(await group.inputValue(), 'permissions')
  const search = page.locator('#portal-message-search')
  await search.fill('admin.permission.audit.read')
  await page.getByRole('button', { name: 'Read the audit log', exact: true }).waitFor()
}

const browser = await chromium.launch()
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  page.setDefaultTimeout(10000)
  const pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.message))
  const document = await page.goto(admin)
  assert.equal(document.status(), 200)
  assert.match(document.headers()['cache-control'], /no-store/u)
  const entryHTML = await document.text()
  assert.match(entryHTML, /admin\.sign_in/u)
  assert.doesNotMatch(entryHTML, /admin\.leave_unsaved|slug_rules/u)
  const publicHTML = await context.request.get(origin)
  assert.equal(publicHTML.status(), 200)
  assert.doesNotMatch(await publicHTML.text(), /admin\.sign_in|admin\.leave_unsaved|slug_rules/u)
  const negotiated = await context.request.get(admin, { headers: { 'Accept-Language': 'kk-KZ, en;q=0.5' } })
  assert.match(await negotiated.text(), /<html[^>]+lang="kk"/u)
  await assertPublishedLocales(page)
  const cookie = await signIn(page, context)
  await verifyWorkspaceGeometry(page)
  await verifyCompactNavigation(page)
  await verifySessionPolicy(page)
  await verifyRatePolicy(page)
  await verifyPermissionTextNavigation(page)
  await verifyNestedNavigation(page)
  await verifyTextSearchAfterResize(page)
  await verifySettingsSave(page, context)
  await verifyCatalogConflictAfterDirectorySave(page, context)
  await verifySlugRuleSettings(page, context)
  await verifyLanguageFlagSettings(page, context)
  await verifyTypeBuilder(page)
  await verifyContentWorkflow(page, context, { type: 'article', label: 'Article', namespace: 'articles', slugBase: 'portal-workflow-verification', title: 'Portal workflow verification', checkLayout: true })
  await verifyContentWorkflow(page, context, { type: 'event', label: 'Event', namespace: 'events', slugBase: 'community-workshop', title: 'Community workshop' })
  await verifyCustomCollection(page, context)
  await verifyEditorialNavigation(page)
  await verifyAccessAdministration(browser, page, origin, assertTheme, assertFit)
  await signOut(page, context, cookie)
  await assertLayouts(page)
  assert.ok(pageErrors.length === 0, 'The admin page logged browser errors')
  console.log('Settings draft and publication, published catalogs, password, TOTP, session reload, logout, revoked-cookie recovery and responsive themes passed')
}
finally {
  await browser.close()
}
