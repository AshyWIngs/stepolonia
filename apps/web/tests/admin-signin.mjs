import assert from 'node:assert/strict'
import process from 'node:process'
import { chromium } from 'playwright'

const endpoint = new URL(process.env.QA_CMS_ORIGIN)
assert.equal(endpoint.protocol, 'https:')
assert.equal(endpoint.hostname, '127.0.0.1')
const { origin } = endpoint
const admin = new URL('/admin', origin).href
const authPath = '/api/v1/admin/auth/'
const sessionCookie = '__Host-sp_session'

function heading(page, name) {
  return page.getByRole('heading', { name, exact: true })
}

function waitForAuth(page, action, status) {
  return page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.origin === origin && url.pathname === authPath + action && response.status() === status
  })
}

async function assertFit(page) {
  const fits = await page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth,
  )
  assert.ok(fits, 'The admin sign-in page must fit without horizontal scrolling')
}

async function assertTheme(page, preference, actual) {
  await page.waitForFunction(({ preference, actual }) =>
    document.documentElement.dataset.cmsTheme === preference &&
    getComputedStyle(document.documentElement).colorScheme === actual,
  { preference, actual })
}

async function assertLayouts(page) {
  const theme = page.getByRole('combobox', { name: 'Theme', exact: true })
  const viewports = [{ width: 1440, height: 900 }, { width: 768, height: 1024 }, { width: 390, height: 844 }]
  const verifyViewport = async (previous, viewport) => {
    await previous
    await page.setViewportSize(viewport)
    const verifyTheme = async (checked, value) => {
      await checked
      await theme.selectOption(value)
      await assertTheme(page, value, value)
      await assertFit(page)
      assert.ok(await page.getByRole('button', { name: 'Continue', exact: true }).isVisible())
      console.log(`CMS sign-in visible at ${viewport.width}×${viewport.height} in ${value} mode`)
    }
    await ['light', 'dark'].reduce(verifyTheme, Promise.resolve())
  }
  await viewports.reduce(verifyViewport, Promise.resolve())
  await page.setViewportSize({ width: 320, height: 844 })
  await assertFit(page)
  await page.reload()
  await heading(page, 'Sign in').waitFor()
  await assertTheme(page, 'dark', 'dark')
  await theme.selectOption('system')
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
  await page.keyboard.type(process.env.QA_CMS_LOGIN)
  await page.keyboard.press('Tab')
  assert.ok(await password.evaluate(element => element === document.activeElement))
  await password.fill('Incorrect test password')
  const denied = waitForAuth(page, 'login', 401)
  await password.press('Enter')
  await denied
  await page.getByRole('alert').waitFor()
  await heading(page, 'Sign in').waitFor()
  assert.equal(await password.inputValue(), '')
  await password.fill(process.env.QA_CMS_PASSWORD)
  const login = waitForAuth(page, 'login', 200)
  await password.press('Enter')
  await login
  const mfaHeading = heading(page, 'Two-step verification')
  await mfaHeading.waitFor()
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
  await code.fill(process.env.QA_CMS_CODE)
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
  const logout = waitForAuth(page, 'logout', 204)
  await page.getByRole('button', { name: 'Sign out', exact: true }).click()
  await logout
  await heading(page, 'Sign in').waitFor()
  assert.ok(!(await context.cookies(origin)).some(cookie => cookie.name === sessionCookie))

  // Simulate a browser that still has a previously valid cookie after revocation.
  await context.clearCookies()
  await context.addCookies([revokedCookie])
  const rejected = waitForAuth(page, 'csrf', 401)
  await page.reload()
  await rejected
  await heading(page, 'Sign in').waitFor()
  assert.ok(!(await context.cookies(origin)).some(cookie => cookie.name === sessionCookie))
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
  await page.getByRole('combobox', { name: 'Język', exact: true }).selectOption('en')
  await heading(page, 'Sign in').waitFor()
  await page.waitForFunction(() => document.documentElement.lang === 'en')
  const cookie = await signIn(page, context)
  await signOut(page, context, cookie)
  await assertLayouts(page)
  assert.ok(pageErrors.length === 0, 'The admin page logged browser errors')
  console.log('CMS password, TOTP, session reload, logout, revoked-cookie recovery and responsive themes passed')
}
finally {
  await browser.close()
}
