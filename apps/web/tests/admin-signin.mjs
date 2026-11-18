import assert from 'node:assert/strict'
import process from 'node:process'
import { chromium } from 'playwright'

const endpoint = new URL(process.env.QA_ADMIN_ORIGIN)
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
  await page.waitForFunction(({ preference, actual }) => {
    const shell = document.querySelector('.admin-shell')
    const card = document.querySelector('.admin-card')
    const shellColor = actual === 'dark' ? 'rgb(20, 28, 26)' : 'rgb(241, 244, 240)'
    const cardColor = actual === 'dark' ? 'rgb(28, 42, 37)' : 'rgb(255, 255, 255)'
    return document.documentElement.dataset.adminTheme === preference &&
      getComputedStyle(document.documentElement).colorScheme === actual &&
      shell && getComputedStyle(shell).backgroundColor === shellColor &&
      card && getComputedStyle(card).backgroundColor === cardColor
  }, { preference, actual })
}

async function assertLayouts(page) {
  const theme = page.getByRole('combobox', { name: 'Theme', exact: true })
  const verifyViewport = async (width, height) => {
    await page.setViewportSize({ width, height })
    const verifyTheme = async value => {
      await theme.selectOption(value)
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
  await password.fill(process.env.QA_ADMIN_PASSWORD)
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

async function readPublicSite(context) {
  const result = await context.request.get(new URL('/api/v1/site?lang=en', origin).href)
  assert.equal(result.status(), 200)
  return result.json()
}

async function verifySettingsPublication(page, context) {
  const oldSite = await readPublicSite(context)
  const newTitle = 'Polacy w Kazachstanie'
  const newMessage = 'Enter your code to open the community portal.'
  await page.getByRole('button', { name: 'Portal settings', exact: true }).click()
  const portalName = page.getByLabel('Portal name', { exact: true })
  assert.equal(await portalName.inputValue(), oldSite.settings.title)
  const message = page.getByLabel('admin.code_help', { exact: true })
  assert.equal(await message.inputValue(), oldSite.messages['admin.code_help'])
  await page.setViewportSize({ width: 320, height: 844 })
  await assertFit(page)
  await portalName.fill(newTitle)
  await message.fill(newMessage)
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  assert.deepEqual(await readPublicSite(context), oldSite)

  await page.getByRole('button', { name: 'Publish draft', exact: true }).click()
  const confirmation = page.getByRole('region', { name: 'Publish the saved settings and interface texts?', exact: true })
  await confirmation.getByText(newTitle, { exact: true }).waitFor()
  await confirmation.getByRole('button', { name: 'Publish draft', exact: true }).click()
  await page.getByText('Settings published.', { exact: true }).waitFor()
  await page.locator('.admin-name').getByText(newTitle, { exact: false }).waitFor()
  const published = await readPublicSite(context)
  assert.notEqual(published.version, oldSite.version)
  assert.equal(published.settings.title, newTitle)
  assert.equal(published.messages['admin.code_help'], newMessage)
  const document = await page.reload()
  assert.equal(document.status(), 200)
  assert.match(await document.text(), /Polacy w Kazachstanie/u)
  await heading(page, 'Signed in').waitFor()
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

async function assertPublishedLocales(page) {
  await heading(page, 'Sign in').waitFor()
  await page.waitForFunction(() => document.documentElement.lang === 'en')
  const language = page.getByRole('combobox', { name: 'Language', exact: true })
  assert.deepEqual(await language.getByRole('option').allTextContents(), ['English', 'Français (rédaction QA)'])
  await language.selectOption('fr')
  await heading(page, 'Connexion à la rédaction').waitFor()
  await page.waitForFunction(() => document.documentElement.lang === 'fr')
  const localized = await page.reload()
  assert.equal(localized.status(), 200)
  assert.match(await localized.text(), /<html[^>]+lang="fr"/u)
  await heading(page, 'Connexion à la rédaction').waitFor()
  const langue = page.getByRole('combobox', { name: 'Langue', exact: true })
  assert.equal(await langue.inputValue(), 'fr')
  await langue.selectOption('en')
  await heading(page, 'Sign in').waitFor()
  await page.waitForFunction(() => document.documentElement.lang === 'en')
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
  await assertPublishedLocales(page)
  const cookie = await signIn(page, context)
  await verifySettingsPublication(page, context)
  await signOut(page, context, cookie)
  await assertLayouts(page)
  assert.ok(pageErrors.length === 0, 'The admin page logged browser errors')
  console.log('Settings draft and publication, published catalogs, password, TOTP, session reload, logout, revoked-cookie recovery and responsive themes passed')
}
finally {
  await browser.close()
}
