import assert from 'node:assert/strict'
import process from 'node:process'
import { chromium } from 'playwright'
import { eachInOrder, verifyAccessAdministration } from './admin-access.mjs'

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
  await message.fill('   ')
  const saveDraft = page.getByRole('button', { name: 'Save draft', exact: true })
  const publishDraft = page.getByRole('button', { name: 'Publish draft', exact: true })
  const confirmation = page.getByRole('region', { name: 'Publish the saved settings and interface texts?', exact: true })
  await saveDraft.click()
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  await publishDraft.click()
  const messageAlert = page.getByRole('alert')
  await messageAlert.waitFor()
  assert.equal(await messageAlert.textContent(), 'Check the portal fields and complete each selected translation.')
  assert.equal(await confirmation.count(), 0)
  assert.deepEqual(await readPublicSite(context), oldSite)

  await message.fill(newMessage)
  await saveDraft.click()
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  assert.deepEqual(await readPublicSite(context), oldSite)

  await publishDraft.click()
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

async function verifyTypeBuilder(page) {
  await page.getByRole('button', { name: 'Type Builder', exact: true }).click()
  const renderer = page.getByRole('combobox', { name: 'Renderer and collection', exact: true })
  await renderer.selectOption('event')
  const initial = page.getByRole('checkbox', { name: 'Grant me access to this new collection', exact: true })
  assert.equal(await initial.isChecked(), false)
  await initial.check()
  await page.getByRole('button', { name: 'Create collection', exact: true }).click()
  await page.getByText('Collection created.', { exact: true }).waitFor()
  await page.getByRole('button', { name: 'Add optional field', exact: true }).click()
  await page.getByLabel('Field key', { exact: true }).fill('subtitle')
  await page.getByRole('checkbox', { name: 'Extend my existing access to the new fields', exact: true }).check()
  await page.getByRole('button', { name: 'Save schema changes', exact: true }).click()
  await page.getByText('Current schema 2', { exact: true }).waitFor()
  await page.getByText('Collection changes saved.', { exact: true }).waitFor()
  const articleSchema = '**/api/v1/admin/content-types/article'
  await page.route(articleSchema, route => route.fulfill({ status: 503, json: { code: 'security_unavailable' } }))
  await renderer.selectOption('article')
  await page.getByRole('alert').waitFor()
  assert.equal(await page.getByRole('button', { name: 'Save schema changes', exact: true }).count(), 0)
  assert.equal(await page.getByRole('button', { name: 'Create collection', exact: true }).count(), 0)
  assert.equal(await page.getByRole('group', { name: 'subtitle', exact: true }).count(), 0)
  await page.unroute(articleSchema)
  await page.getByRole('button', { name: 'Reload collection', exact: true }).click()
  await page.getByRole('group', { name: 'title', exact: true }).waitFor()
  assert.equal(await page.getByRole('group', { name: 'subtitle', exact: true }).count(), 0)
  await page.getByRole('button', { name: 'Close', exact: true }).click()
}

async function verifyContentWorkflow(page, context, { type, namespace, slug, title, checkLayout = false }) {
  await page.getByRole('button', { name: 'Content', exact: true }).click()
  const collection = page.getByRole('combobox', { name: 'Collection', exact: true })
  await collection.selectOption(type)
  const create = page.getByRole('button', { name: 'Create draft', exact: true, disabled: false })
  await create.waitFor()
  await create.focus()
  await page.keyboard.press('Enter')
  const editor = page.getByRole('region', { name: 'Draft editor', exact: true })
  await editor.getByLabel(/^Title(?: \(|$)/).waitFor()
  await editor.getByLabel(/^Title(?: \(|$)/).fill(title)
  await editor.getByLabel(/^URL slug(?: \(|$)/).fill(slug)
  const bodyField = editor.getByLabel(/^Body(?: \(|$)/)
  if (await bodyField.count()) await bodyField.fill('An editorial draft.')
  const optionalField = editor.getByLabel('subtitle', { exact: true })
  if (await optionalField.count()) await optionalField.fill('Private editor text.')
  if (checkLayout) await assertEditorLayouts(page, editor)
  await editor.getByRole('button', { name: 'Save draft', exact: true }).click()
  await editor.getByText('Draft saved.', { exact: true }).waitFor()

  const apiPath = `/api/v1/content/${type}/${slug}?language=en`
  const publicPath = `/en/${namespace}/${slug}`
  const request = path => context.request.get(new URL(path, origin).href)
  assert.equal((await request(apiPath)).status(), 404)
  await editor.getByRole('button', { name: 'Mark shared fields ready', exact: true }).click()
  await editor.getByText('Shared fields are ready.', { exact: true }).waitFor()
  await editor.getByRole('button', { name: 'Mark translation ready', exact: true }).click()
  await editor.getByText('This translation is ready.', { exact: true }).waitFor()

  await editor.getByRole('button', { name: 'Publish translations', exact: true }).click()
  const publish = editor.getByRole('region', { name: 'Publish the selected translations?', exact: true })
  await publish.getByRole('button', { name: 'Confirm', exact: true }).click()
  const link = editor.getByRole('link', { name: publicPath, exact: true })
  await link.waitFor()
  assert.equal(await link.getAttribute('href'), publicPath)
  const published = await request(apiPath)
  assert.equal(published.status(), 200)
  const body = await published.json()
  assert.equal(body.localized.title.value, title)
  assert.equal(Object.hasOwn(body.localized, 'subtitle'), false)
  const rendered = await request(publicPath)
  assert.equal(rendered.status(), 200)
  assert.ok((await rendered.text()).includes(title))

  await editor.getByRole('button', { name: 'Unpublish', exact: true }).click()
  const removal = editor.getByRole('region', { name: 'Remove this publication and clear readiness?', exact: true })
  await removal.getByRole('button', { name: 'Confirm', exact: true }).click()
  await editor.getByText('Publication removed.', { exact: true }).waitFor()
  assert.equal((await request(apiPath)).status(), 404)
  assert.equal((await request(publicPath)).status(), 404)
  await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
  await page.getByRole('button', { name: 'Close', exact: true }).click()
}

async function assertEditorLayouts(page, editor) {
  const theme = page.getByRole('combobox', { name: 'Theme', exact: true })
  const verifyViewport = async (width, height) => {
    await page.setViewportSize({ width, height })
    const verifyTheme = async value => {
      await theme.selectOption(value)
      await assertTheme(page, value, value)
      await assertFit(page)
      assert.ok(await editor.getByRole('button', { name: 'Save draft', exact: true }).isVisible())
      console.log(`Draft editor visible at ${width}×${height} in ${value} mode`)
    }
    await verifyTheme('light')
    await verifyTheme('dark')
  }
  await verifyViewport(1440, 900)
  await verifyViewport(768, 1024)
  await verifyViewport(390, 844)
  await page.setViewportSize({ width: 320, height: 844 })
  await assertFit(page)
}

async function assertPolishDraft(locale, title, expected) {
  await title.waitFor()
  assert.equal(await locale.inputValue(), 'pl')
  assert.equal(await title.inputValue(), expected)
}

async function verifyEditorialNavigation(page) {
  await page.locator('#admin-language').selectOption('pl')
  await page.getByRole('button', { name: 'Materiały', exact: true }).click()
  const collection = page.getByRole('combobox', { name: 'Kolekcja', exact: true })
  await collection.selectOption('article')
  await page.getByRole('button', { name: 'Utwórz szkic', exact: true }).click()
  const editor = page.getByRole('region', { name: 'Edytor szkicu', exact: true })
  const locale = editor.getByLabel('Język', { exact: true })
  const title = editor.getByLabel(/^Tytuł/u)
  const original = 'Spacer po Ałmaty'
  const updated = 'Jesienny spacer po Ałmaty'
  await locale.selectOption('pl')
  await title.fill(original)
  await editor.getByRole('button', { name: 'Zapisz szkic', exact: true }).click()
  await editor.getByRole('status').getByText('Zapisano szkic.').waitFor()
  await editor.getByRole('button', { name: 'Wróć do szkiców', exact: true }).click()
  await page.getByRole('button', { name: /Portal workflow verification/u }).waitFor()
  await page.getByRole('button', { name: /Spacer po Ałmaty.*Polski.*Brak publikacji/u }).click()
  await assertPolishDraft(locale, title, original)
  await locale.selectOption('en')
  assert.equal(await title.inputValue(), '')
  await editor.getByRole('button', { name: 'Wróć do szkiców', exact: true }).click()
  await page.getByRole('button', { name: /Spacer po Ałmaty.*Polski.*Brak publikacji/u }).click()
  assert.equal(await locale.inputValue(), 'pl')

  await page.setViewportSize({ width: 390, height: 844 })
  await title.fill(updated)
  const settings = page.getByRole('button', { name: 'Ustawienia portalu', exact: true })
  await settings.click()
  const leave = page.getByRole('dialog')
  await leave.getByRole('button', { name: 'Wróć do edycji', exact: true }).click()
  assert.equal(await title.inputValue(), updated)
  assert.ok(await settings.evaluate(element => element === document.activeElement))
  await editor.getByRole('button', { name: 'Zapisz szkic', exact: true }).click()
  await editor.getByRole('status').getByText('Zapisano szkic.').waitFor()
  await editor.getByRole('button', { name: 'Wróć do szkiców', exact: true }).click()
  const row = page.getByRole('button', { name: /Jesienny spacer po Ałmaty.*Polski.*Brak publikacji/u })
  await row.waitFor()
  assert.ok(await row.evaluate(element => element === document.activeElement))
  await row.click()
  await assertPolishDraft(locale, title, updated)
  await editor.getByText('Brak opublikowanych tłumaczeń.', { exact: true }).waitFor()

  await title.fill('Niezapisana zmiana')
  await settings.click()
  await leave.getByRole('button', { name: 'Odrzuć zmiany', exact: true }).click()
  await page.getByRole('heading', { name: 'Ustawienia portalu', level: 1, exact: true }).waitFor()
  await page.locator('#admin-language').selectOption('en')
  await heading(page, 'Portal settings').first().waitFor()
  await page.getByRole('button', { name: 'Close', exact: true }).click()
}

async function assertPublishedLocales(page) {
  await heading(page, 'Sign in').waitFor()
  await page.waitForFunction(() => document.documentElement.lang === 'en')
  const language = page.getByRole('combobox', { name: 'Language', exact: true })
  assert.deepEqual(await language.getByRole('option').evaluateAll(options => options.map(option => option.value)), ['en', 'kk', 'pl', 'ru'])
  await eachInOrder([['kk', 'Кіру'], ['ru', 'Вход'], ['pl', 'Zaloguj się']], async ([locale, name]) => {
    await page.locator('#admin-language').selectOption(locale)
    await heading(page, name).waitFor()
    await page.waitForFunction(value => document.documentElement.lang === value, locale)
  })
  const localized = await page.reload()
  assert.equal(localized.status(), 200)
  assert.match(await localized.text(), /<html[^>]+lang="pl"/u)
  await heading(page, 'Zaloguj się').waitFor()
  const polish = page.getByRole('combobox', { name: 'Język', exact: true })
  assert.equal(await polish.inputValue(), 'pl')
  await polish.selectOption('en')
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
  await verifyTypeBuilder(page)
  await verifyContentWorkflow(page, context, { type: 'article', namespace: 'articles', slug: 'portal-workflow', title: 'Portal workflow verification', checkLayout: true })
  await verifyContentWorkflow(page, context, { type: 'event', namespace: 'events', slug: 'community-workshop', title: 'Community workshop' })
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
