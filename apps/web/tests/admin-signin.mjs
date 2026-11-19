import assert from 'node:assert/strict'
import process from 'node:process'
import { chromium } from 'playwright'
import { assertEditorMutationBlocksNavigation, assertSavedAddress, eachInOrder, expectedTitleSlug, verifyAccessAdministration, waitForContentMutation } from './admin-access.mjs'
import { signInAfterAnotherTabRefreshesPreauth } from './admin-preauth.mjs'
import { assertStablePreferences, chooseTheme, headerGeometry } from './header-preferences.mjs'

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

async function readPublicSite(context) {
  const result = await context.request.get(new URL('/api/v1/site?lang=en', origin).href)
  assert.equal(result.status(), 200)
  return result.json()
}

async function reopenMissingText(page, sourceText) {
  const messageAlert = page.getByRole('alert')
  await messageAlert.waitFor()
  assert.equal(await messageAlert.textContent(), 'Fill in the missing texts or remove this language.')
  await page.getByRole('button', { name: 'Back to texts', exact: true }).click()
  assert.ok(await page.getByRole('checkbox', { name: 'Only untranslated texts', exact: true }).isChecked())
  assert.equal(await page.getByRole('searchbox', { name: 'Search texts', exact: true }).inputValue(), '')
  await page.getByRole('list', { name: 'Text to edit', exact: true }).getByRole('button', { name: sourceText, exact: true }).click()
}

async function verifySettingsSave(page, context) {
  const oldSite = await readPublicSite(context)
  const newTitle = 'Polacy w Kazachstanie'
  const newMessage = 'Enter your code to open the community portal.'
  await page.getByRole('button', { name: 'Portal settings', exact: true }).click()
  const portalName = page.getByLabel('Portal name', { exact: true })
  assert.equal(await portalName.inputValue(), oldSite.settings.title)
  const save = page.getByRole('button', { name: 'Save changes', exact: true })
  assert.equal(await save.count(), 0)
  assert.equal(await page.getByRole('button', { name: 'Close', exact: true }).count(), 0)
  await page.setViewportSize({ width: 320, height: 844 })
  await assertFit(page)
  await portalName.fill(newTitle)
  await page.getByRole('button', { name: 'Appearance', exact: true }).click()
  assert.ok(await page.getByLabel('Choose accent color', { exact: true }).isVisible())
  assert.equal(await portalName.count(), 0)
  await page.getByRole('button', { name: 'Portal details', exact: true }).click()
  assert.equal(await portalName.inputValue(), newTitle)
  const message = await findSettingsTranslation(page, oldSite.messages['admin.code_help'])
  await message.fill('   ')
  assert.equal(await save.count(), 1)
  await save.click()
  await reopenMissingText(page, oldSite.messages['admin.code_help'])
  assert.deepEqual(await readPublicSite(context), oldSite)

  await message.fill(newMessage)
  await assertEditorMutationBlocksNavigation(page, '**/api/v1/admin/settings/publication', 'PUT', () => save.click(), ['Content', 'Sign out'])
  await page.getByText('Settings saved.', { exact: true }).waitFor()
  assert.equal(await save.count(), 0)
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

async function verifySlugRuleSettings(page, context) {
  const snapshot = await readPublicSite(context)
  const polish = snapshot.slug_rules.filter(rule => rule.language_code === 'pl')
  assert.deepEqual(Object.fromEntries(polish.map(rule => [rule.character, rule.replacement])),
    { ą: 'a', ć: 'c', ę: 'e', ł: 'l', ń: 'n', ó: 'o', ś: 's', ź: 'z', ż: 'z' })
  await page.getByRole('button', { name: 'Languages and translations', exact: true }).click()
  const language = page.getByLabel('Rule language', { exact: true })
  const search = page.getByRole('searchbox', { name: 'Search rules', exact: true })
  const replacement = page.getByLabel('Replacement', { exact: true })
  const save = page.getByRole('button', { name: 'Save changes', exact: true })
  await language.selectOption('pl')
  await search.fill('ł')
  assert.equal(await replacement.inputValue(), 'l')
  assert.equal(await save.count(), 0)
  await replacement.fill('ll')
  const publishedReplacement = async () => (await readPublicSite(context)).slug_rules.find(rule =>
    rule.language_code === 'pl' && rule.character === 'ł')?.replacement
  assert.equal(await publishedReplacement(), 'l')
  await save.click()
  await page.getByText('Settings saved.', { exact: true }).waitFor()
  assert.equal(await publishedReplacement(), 'll')
}

async function waitForFlag(control, svg) {
  if (!svg) {
    await control.locator('.language-flag svg').waitFor()
    return
  }
  const source = `data:image/svg+xml,${encodeURIComponent(svg)}`
  await control.locator(`.language-flag img[src=${JSON.stringify(source)}]`).evaluate(image => image.decode())
}

async function verifyLanguageFlagSettings(page, context) {
  const source = page.getByRole('textbox', { name: 'Flag SVG', exact: true })
  const save = page.getByRole('button', { name: 'Save changes', exact: true })
  const trigger = page.getByTestId('language-switcher').locator('summary')
  const original = await source.inputValue()
  const invalid = '<svg viewBox="0 0 24 16"><path d="M0 0 L"/></svg>'
  await source.fill(invalid)
  await save.click()
  await page.getByRole('alert').getByText('This flag could not be saved. Check the SVG and try again.').waitFor()
  assert.equal(await source.inputValue(), invalid)
  assert.ok(await source.evaluate(element => element === document.activeElement))
  await waitForFlag(trigger, original)

  await source.fill('<svg viewBox="0 0 24 16"><rect width="24" height="16" fill="#6a158c"/></svg>')
  await save.click()
  await page.getByText('Settings saved.', { exact: true }).waitFor()
  const { language_flags: flags } = await readPublicSite(context)
  assert.match(flags.en, /#6a158c/u)
  await waitForFlag(trigger, flags.en)
  await waitForFlag(page.getByTestId('language-flag-preview'), flags.en)
  const publicPage = await context.newPage()
  try {
    await publicPage.goto(`${origin}/en/articles`)
    const publicTrigger = publicPage.getByTestId('language-switcher').locator('summary')
    await waitForFlag(publicTrigger, flags.en)

    await page.getByRole('button', { name: 'Use globe', exact: true }).click()
    await save.click()
    await page.getByText('Settings saved.', { exact: true }).waitFor()
    assert.equal((await readPublicSite(context)).language_flags.en, undefined)
    await waitForFlag(trigger)
    await publicPage.reload()
    await waitForFlag(publicTrigger)
  }
  finally {
    await publicPage.close()
  }
  await source.fill(original)
  await save.click()
  await page.getByText('Settings saved.', { exact: true }).waitFor()
}

async function verifyNestedNavigation(page) {
  await page.setViewportSize({ width: 1920, height: 1080 })
  const sidebar = page.locator('.admin-navigation')
  const settings = sidebar.getByRole('button', { name: 'Portal settings', exact: true })
  const localization = sidebar.getByRole('button', { name: 'Languages and translations', exact: true })
  const details = sidebar.getByRole('button', { name: 'Portal details', exact: true })
  const appearance = sidebar.getByRole('button', { name: 'Appearance', exact: true })
  await settings.press('Enter')
  assert.equal(await settings.getAttribute('aria-expanded'), 'true')
  assert.equal(await appearance.isVisible(), true)
  assert.equal(await details.getAttribute('aria-current'), 'page')
  await page.keyboard.press('Tab')
  assert.ok(await details.evaluate(element => element === document.activeElement))
  await appearance.click()
  await heading(page, 'Appearance').waitFor()
  assert.equal(await appearance.getAttribute('aria-current'), 'page')
  await localization.click()
  assert.equal(await settings.getAttribute('aria-expanded'), 'false')
  assert.equal(await localization.getAttribute('aria-expanded'), 'true')
  assert.equal(await appearance.isVisible(), false)
  assert.equal(await sidebar.getByRole('button', { name: 'Languages', exact: true }).getAttribute('aria-current'), 'page')
  assert.equal(await sidebar.locator('[aria-current="page"]:visible').count(), 1)
  await page.getByRole('button', { name: 'Content', exact: true }).click()
}

async function verifyTextSearchAfterResize(page) {
  await page.setViewportSize({ width: 1920, height: 1080 })
  await selectInterfaceLanguage(page, 'ru')
  await page.getByRole('button', { name: 'Языки и переводы', exact: true }).click()
  await page.getByRole('button', { name: 'Тексты интерфейса', exact: true }).click()
  const search = page.getByRole('searchbox', { name: 'Поиск текстов', exact: true })
  const signOutText = page.getByRole('list', { name: 'Текст для редактирования', exact: true }).getByRole('button', { name: 'Выйти', exact: true })
  await search.fill('Выйти')
  await signOutText.click()
  const list = await page.locator('.catalog-list-pane').boundingBox()
  const editor = await page.locator('.catalog-editor').boundingBox()
  assert.ok(list && editor && editor.x >= list.x + list.width && editor.width > 760)
  await page.getByRole('button', { name: 'К списку текстов', exact: true }).click()
  await search.clear()
  await page.setViewportSize({ width: 390, height: 700 })
  await page.evaluate(() => scrollTo(0, 0))
  const box = await search.boundingBox()
  assert.ok(box && box.y >= 0 && box.y + box.height <= 700)
  await search.focus()
  await page.keyboard.insertText('Выйти')
  await signOutText.click()
  await page.getByRole('button', { name: 'К списку текстов', exact: true }).click()
  assert.equal(await search.inputValue(), 'Выйти')
  await selectInterfaceLanguage(page, 'en')
  await page.getByRole('button', { name: 'Content', exact: true }).click()
}

async function openIdentityChallenge(page, path, button) {
  let csrf
  const requireConfirmation = async route => {
    if (route.request().method() !== 'POST') return route.continue()
    csrf = route.request().headers()['x-csrf-token']
    return route.fulfill({ status: 403, json: { code: 'step_up_required' } })
  }
  const dialog = page.getByRole('dialog', { name: 'Confirm your identity', exact: true })
  await page.route(path, requireConfirmation)
  try {
    await button.click()
    await dialog.waitFor()
    assert.ok(await page.locator('.admin-navigation').isVisible())
    assert.ok(await dialog.getByLabel('Password', { exact: true }).evaluate(element => element === document.activeElement))
    await assertFit(page)
    return { dialog, csrf }
  } finally {
    await page.unroute(path, requireConfirmation)
  }
}

async function confirmIdentityInBrowser(page, dialog, csrf) {
  const path = '**/api/v1/admin/auth/step-up'
  const password = dialog.getByLabel('Password', { exact: true })
  const code = dialog.getByLabel('Verification code', { exact: true })
  const confirm = dialog.getByRole('button', { name: 'Confirm identity', exact: true })
  const reject = route => route.fulfill({ status: 401, json: { code: 'invalid_credentials' } })
  await page.route(path, reject, { times: 1 })
  await password.fill('Invalid test password')
  await code.fill('123456')
  await confirm.click()
  await dialog.getByRole('alert').getByText('We could not verify your credentials. Check your entries and try again.').waitFor()
  assert.equal(await password.inputValue(), '')
  assert.equal(await code.inputValue(), '')
  assert.ok(csrf, 'The original protected request carried a CSRF token')
  // Exercise the browser workflow independently of the server's TOTP replay checks.
  await page.route(path, (route) => {
    assert.deepEqual(route.request().postDataJSON(), { password: 'Valid test password', code: '654321' })
    return route.fulfill({ status: 200, json: { csrf_token: csrf } })
  }, { times: 1 })
  await password.evaluate((element) => {
    element.value = 'Valid test password'
    element.form.elements.namedItem('code').value = '654321'
  })
  await confirm.click()
  await dialog.waitFor({ state: 'hidden' })
  await page.getByRole('status').getByText('Identity confirmed. Repeat the action to continue.').waitFor()
}

async function verifyCatalogGroups(page) {
  const group = page.getByRole('combobox', { name: 'Section', exact: true })
  const search = page.getByRole('searchbox', { name: 'Search texts', exact: true })
  const results = page.getByRole('list', { name: 'Text to edit', exact: true })
  assert.equal(await group.inputValue(), 'all')
  await group.selectOption({ label: 'Collection structure' })
  await search.fill('admin.registry_article')
  await results.getByRole('button', { name: 'Article', exact: true }).waitFor()
  assert.equal(await results.getByText('admin.registry_article', { exact: true }).count(), 0)
  await search.fill('admin.sign_out')
  await page.getByText('No matching texts.', { exact: true }).waitFor()
  await group.selectOption({ label: 'All texts' })
  assert.equal(await search.inputValue(), 'admin.sign_out')
  await results.getByRole('button', { name: 'Sign out', exact: true }).waitFor()
  await group.selectOption({ label: 'Users and access' })
  await search.fill('Multiple domains')
  await results.getByRole('button', { name: 'Multiple domains Accounts', exact: true }).waitFor()
  assert.ok(await results.getByRole('button', { name: 'Multiple domains Roles', exact: true }).isVisible())
  await group.selectOption({ label: 'All texts' })
  await search.clear()
}

async function findSettingsTranslation(page, sourceText) {
  await page.getByRole('button', { name: 'Languages and translations', exact: true }).click()
  await page.getByRole('button', { name: 'Interface texts', exact: true }).click()
  await verifyCatalogGroups(page)
  const language = page.getByRole('combobox', { name: 'Language to edit', exact: true })
  assert.equal(await language.inputValue(), 'en')
  const search = page.getByRole('searchbox', { name: 'Search texts', exact: true })
  await selectInterfaceLanguage(page, 'pl')
  assert.ok(await page.getByRole('button', { name: 'Wyloguj się', exact: true }).isVisible())
  await page.getByRole('searchbox').fill('Wyloguj się')
  await page.getByRole('list', { name: 'Tekst do edycji', exact: true }).getByRole('button', { name: 'Wyloguj się', exact: true }).waitFor()
  await selectInterfaceLanguage(page, 'en')
  assert.equal(await language.inputValue(), 'en')
  await search.fill(sourceText)
  assert.equal(await search.inputValue(), sourceText)
  await search.press('Enter')
  assert.equal(await page.getByText('Settings saved.', { exact: true }).count(), 0)
  await page.getByRole('list', { name: 'Text to edit', exact: true }).getByRole('button', { name: sourceText, exact: true }).click()
  const message = page.getByLabel('Translation', { exact: true })
  assert.equal(await message.inputValue(), sourceText)
  assert.equal(await page.getByRole('textbox', { name: 'Translation', exact: true }).count(), 1)
  assert.ok(await message.evaluate(element => element === document.activeElement))
  return message
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

async function openSection(page, name) {
  const trigger = page.locator('.admin-navigation').getByRole('button', { name, exact: true })
  if (await trigger.getAttribute('aria-expanded') !== 'true') await trigger.click()
}
function collectionButton(page, domain, label) {
  return page.locator(`#admin-nav-${domain}`).getByRole('button', { name: label, exact: true })
}

async function verifyTypeBuilder(page) {
  await openSection(page, 'Structure')
  await collectionButton(page, 'registry', 'Article').click()
  const titleField = page.getByRole('group', { name: 'Title', exact: true })
  await titleField.waitFor()
  assert.equal(await titleField.locator('summary').getByText('title', { exact: true }).count(), 0)
  assert.equal(await page.getByRole('group', { name: 'URL slug', exact: true }).count(), 0)
  assert.equal(await page.locator('.builder-field summary').getByText('slug', { exact: true }).count(), 0)
  await collectionButton(page, 'registry', 'Collections').click()
  await page.getByRole('button', { name: 'Create collection', exact: true }).click()
  const renderer = page.getByRole('combobox', { name: 'Collection template', exact: true })
  await renderer.selectOption({ label: 'Event' })
  assert.equal(await renderer.isEnabled(), true)
  const initial = page.getByRole('checkbox', { name: 'Grant me access to this new collection', exact: true })
  assert.equal(await initial.isChecked(), false)
  await initial.check()
  assert.equal(await renderer.isDisabled(), true)
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click()
  assert.equal(await initial.isChecked(), false)
  assert.ok(await renderer.evaluate(element => element === document.activeElement))
  await initial.check()
  const create = page.getByRole('button', { name: 'Create collection', exact: true })
  await verifyCollectionAccessChallenge(page, initial, create)
  await create.click()
  await page.getByText('Collection created.', { exact: true }).waitFor()
  assert.equal(await collectionButton(page, 'registry', 'Event').getAttribute('aria-current'), 'page')
  await page.getByRole('button', { name: 'Add optional field', exact: true }).click()
  const key = page.getByLabel('Field key', { exact: true })
  assert.ok(await key.evaluate(element => element === document.activeElement))
  await key.fill('subtitle')
  await page.getByRole('checkbox', { name: 'Extend my existing access to the new fields', exact: true }).check()
  const save = page.getByRole('button', { name: 'Save collection changes', exact: true })
  await assertEditorMutationBlocksNavigation(page, '**/api/v1/admin/content-types/event', 'PATCH', () => save.click(), ['Content', 'Sign out'])
  await page.getByText('Current schema 2', { exact: true }).waitFor()
  await page.getByText('Collection changes saved.', { exact: true }).waitFor()
  await page.getByRole('group', { name: 'subtitle', exact: true }).waitFor()
  await verifyFieldTranslations(page, save)
  const articleSchema = '**/api/v1/admin/content-types/article'
  await page.route(articleSchema, route => route.fulfill({ status: 503, json: { code: 'security_unavailable' } }))
  await collectionButton(page, 'registry', 'Article').click()
  await page.getByRole('alert').waitFor()
  assert.equal(await page.getByRole('button', { name: 'Save collection changes', exact: true }).count(), 0)
  assert.equal(await page.getByRole('button', { name: 'Create collection', exact: true }).count(), 0)
  assert.equal(await page.locator('summary').getByText('subtitle', { exact: true }).count(), 0)
  await page.unroute(articleSchema)
  await page.getByRole('button', { name: 'Reload collection', exact: true }).click()
  await page.getByRole('group', { name: 'Title', exact: true }).waitFor()
  assert.equal(await page.locator('summary').getByText('subtitle', { exact: true }).count(), 0)
  assert.equal(await page.getByRole('button', { name: 'Close', exact: true }).count(), 0)
}

async function createNamedCollection(page, name, configure) {
  await openSection(page, 'Structure')
  await collectionButton(page, 'registry', 'Collections').click()
  const create = page.getByRole('button', { name: 'Create collection', exact: true })
  await create.click()
  await page.getByLabel('Collection name', { exact: true }).fill(name)
  if (configure) await configure()
  await page.getByRole('checkbox', { name: 'Grant me access to this new collection', exact: true }).check()
  const response = waitForContentMutation(page, '/api/v1/admin/content-types', 'POST', 201)
  await create.click()
  const created = await (await response).json()
  await page.getByText('Collection created.', { exact: true }).waitFor()
  assert.equal(await collectionButton(page, 'registry', name).getAttribute('aria-current'), 'page')
  return created
}

async function verifyCustomCollection(page, context) {
  const name = 'Community notices'
  const svg = '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="3"/></svg>'
  const created = await createNamedCollection(page, name, async () => {
    await page.getByLabel('Collection icon (SVG)', { exact: true }).fill(svg)
  })
  const structureIcon = collectionButton(page, 'registry', name).locator('.admin-icon--custom')
  await structureIcon.waitFor()
  const mask = await structureIcon.evaluate(icon => getComputedStyle(icon).maskImage)
  assert.match(mask, /^url\("data:image\/svg\+xml/iu)
  assert.equal(await page.getByTestId('collection-icon-preview').locator('.admin-icon--custom').count(), 1)
  const available = await page.evaluate(async () => {
    const response = await fetch('/api/v1/admin/content-types?offset=0', { cache: 'no-store' })
    if (!response.ok) throw new Error(`Collection inventory returned ${response.status}`)
    return response.json()
  })
  assert.ok(available.items.some(item => item.type_key === created.type_key && item.display_name === name))
  await page.route('**/api/v1/admin/content-types?offset=0', route => route.fulfill({
    status: 200, json: { ...available, next_offset: 100 },
  }), { times: 1 })
  await openSection(page, 'Content')
  const contentIcon = collectionButton(page, 'content', name).locator('.admin-icon--custom')
  await contentIcon.waitFor()
  assert.equal(await contentIcon.evaluate(icon => getComputedStyle(icon).maskImage), mask)
  const target = await verifyContentWorkflow(page, context, {
    type: created.type_key, namespace: created.route_namespace, label: name, slugBase: 'community-notice',
    title: 'Community notice', keepPublished: true,
  })
  await verifyRelatedCollectionSSR(page, context, name, target)

  await openSection(page, 'Structure')
  await collectionButton(page, 'registry', name).click()
  const disable = page.getByRole('button', { name: 'Disable collection', exact: true })
  await disable.click()
  let dialog = page.getByRole('dialog', { name: 'Disable collection', exact: true })
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.equal(await collectionButton(page, 'registry', name).isVisible(), true)
  await disable.click()
  await dialog.getByRole('button', { name: 'Disable collection', exact: true }).click()
  await page.getByRole('status').getByText('Collection disabled.', { exact: true }).waitFor()
  assert.equal(await collectionButton(page, 'registry', name).count(), 0)
  await openSection(page, 'Content')
  await collectionButton(page, 'content', 'Article').waitFor()
  assert.equal(await collectionButton(page, 'content', name).count(), 0)
  assert.equal((await context.request.get(new URL(target.path, origin).href)).status(), 200)

  await openSection(page, 'Structure')
  await page.getByText('Disabled collections', { exact: true }).click()
  await page.locator('.admin-collection-overview').getByRole('button', { name, exact: true }).click()
  await page.getByRole('button', { name: 'Enable collection', exact: true }).click()
  dialog = page.getByRole('dialog', { name: 'Enable collection', exact: true })
  await dialog.getByRole('button', { name: 'Enable collection', exact: true }).click()
  await page.getByRole('status').getByText('Collection enabled.', { exact: true }).waitFor()
  await openSection(page, 'Content')
  await collectionButton(page, 'content', name).click()
  await page.getByRole('button', { name: /Community notice.*English/u }).waitFor()
  await verifyCollectionIconReset(page, name, created, mask)
}

async function verifyCollectionIconReset(page, name, collection, previousMask) {
  await openSection(page, 'Structure')
  await collectionButton(page, 'registry', name).click()
  const input = page.getByLabel('Collection icon (SVG)', { exact: true })
  const save = page.getByRole('button', { name: 'Save collection changes', exact: true })
  const path = `/api/v1/admin/content-types/${collection.type_key}`
  const unsupported = '<svg viewBox="0 0 24 24"><title>Sample</title><circle cx="12" cy="12" r="8"/></svg>'
  await input.fill(unsupported)
  const rejected = waitForContentMutation(page, path, 'PATCH', 400)
  await save.click()
  assert.equal((await (await rejected).json()).code, 'invalid_collection_icon')
  await page.getByText('Use a valid SVG with a viewBox, simple shapes, and up to 4 KB of code.', { exact: true }).waitFor()
  assert.equal(await input.inputValue(), unsupported)
  const icon = collectionButton(page, 'registry', name).locator('.admin-icon--custom')
  assert.equal(await icon.evaluate(element => getComputedStyle(element).maskImage), previousMask)

  await page.getByRole('button', { name: 'Use default icon', exact: true }).click()
  const reset = waitForContentMutation(page, path, 'PATCH', 200)
  await save.click()
  const result = await (await reset).json()
  assert.equal(result.icon_svg, '')
  assert.equal(result.schema_no, collection.schema_no)
  await collectionButton(page, 'registry', name).locator('svg.admin-icon').waitFor()
  await openSection(page, 'Content')
  await collectionButton(page, 'content', name).locator('svg.admin-icon').waitFor()
}

async function verifyRelatedCollectionSSR(page, context, targetName, target) {
  const sourceName = 'Notice directory'
  const source = await createNamedCollection(page, sourceName, async () => {
    await page.getByRole('button', { name: 'Add optional field', exact: true }).click()
    const field = page.locator('.builder-field').last()
    await field.locator('input[id^="registry-key-"]').fill('related_notice')
    await field.locator('select[id^="registry-kind-"]').selectOption('relation')
    await field.locator('select[id^="registry-visibility-"]').selectOption('public')
    await field.locator('select[id^="registry-target-"]').selectOption({ label: targetName })
  })
  await createContentDraftWithKeyboard(page, source.type_key, sourceName)
  const editor = page.getByRole('region', { name: 'Draft editor', exact: true })
  await editor.getByLabel(/^Title(?: \(|$)/).fill('Notice directory')
  await editor.getByLabel('related_notice', { exact: true }).fill(target.id)
  await editor.getByRole('button', { name: 'Save draft', exact: true }).click()
  await editor.getByText('Draft saved.', { exact: true }).waitFor()
  await editor.getByRole('button', { name: 'Publish translations', exact: true }).click()
  const published = editor.getByRole('link', { name: 'English', exact: true })
  await published.waitFor()
  const rendered = await context.request.get(new URL(await published.getAttribute('href'), origin).href)
  assert.equal(rendered.status(), 200)
  assert.ok((await rendered.text()).includes(`href="${target.path}"`))
  await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
}

async function verifyFieldTranslations(page, save) {
  const field = page.getByRole('group', { name: 'Title', exact: true })
  const summary = field.locator('summary')
  await summary.click()
  assert.ok(await summary.getByText('Required', { exact: true }).isVisible())
  const language = page.getByLabel('Field translation language', { exact: true })
  const label = field.getByLabel('Field label', { exact: true })
  await language.selectOption('ru')
  await label.fill('Заголовок события')
  await language.selectOption('kk')
  await label.fill('Іс-шара атауы')
  await language.selectOption('ru')
  assert.equal(await label.inputValue(), 'Заголовок события')
  assert.equal(await page.locator('html').getAttribute('lang'), 'en')
  await save.click()
  await page.getByText('Collection changes saved.', { exact: true }).waitFor()
  await page.getByText('Current schema 2', { exact: true }).waitFor()
}

async function createContentDraftWithKeyboard(page, type, label) {
  await openSection(page, 'Content')
  await collectionButton(page, 'content', label).click()
  const create = page.getByRole('button', { name: 'Create draft', exact: true, disabled: false })
  await create.waitFor()
  await create.focus()
  const creating = waitForContentMutation(page, `/api/v1/admin/content/${type}`, 'POST', 201)
  await assertEditorMutationBlocksNavigation(page, `**/api/v1/admin/content/${type}`, 'POST',
    () => page.keyboard.press('Enter'), ['Portal settings', 'Sign out'])
  return (await creating).json()
}

async function verifyContentWorkflow(page, context, { type, namespace, label, slugBase, title, checkLayout = false, keepPublished = false }) {
  const { item_id: id } = await createContentDraftWithKeyboard(page, type, label)
  const slug = expectedTitleSlug(slugBase, id)
  const editor = page.getByRole('region', { name: 'Draft editor', exact: true })
  await editor.getByLabel(/^Title(?: \(|$)/).waitFor()
  await editor.getByText('Fill in the translation and save the draft.', { exact: true }).waitFor()
  assert.equal(await editor.getByRole('button', { name: 'Mark shared fields ready', exact: true }).count(), 0)
  await editor.getByLabel(/^Title(?: \(|$)/).fill(title)
  assert.equal(await editor.getByLabel(/^URL slug(?: \(|$)/).count(), 0)
  const save = editor.getByRole('button', { name: 'Save draft', exact: true })
  const bodyField = editor.getByLabel(/^Body(?: \(|$)/)
  if (type === 'article') await verifyIncompletePublication(editor, save, bodyField)
  if (await bodyField.count()) await bodyField.fill('An editorial draft.')
  const optionalField = editor.getByLabel('subtitle', { exact: true })
  if (await optionalField.count()) await optionalField.fill('Private editor text.')
  if (checkLayout) await assertEditorLayouts(page, editor)
  if (keepPublished) await verifyDraftSurvivesMenuError(page, editor, title)
  await assertSavedAddress(page, type, id, save, 'en', slug)
  await editor.getByText('Draft saved.', { exact: true }).waitFor()

  const apiPath = `/api/v1/content/${type}/${slug}?language=en`
  const publicPath = `/en/${namespace}/${slug}`
  const request = path => context.request.get(new URL(path, origin).href)
  assert.equal((await request(apiPath)).status(), 404)
  if (type === 'event') await addSecondTranslation(page, editor, id)
  else await interruptPublicationRefresh(page, type, id)
  await editor.getByRole('button', { name: 'Publish translations', exact: true }).click()
  const publish = editor.getByRole('region', { name: 'Publish the selected translations?', exact: true })
  if (type === 'event') {
    await publish.getByRole('checkbox', { name: 'Polski', exact: true }).uncheck()
    await publish.getByRole('checkbox', { name: 'English', exact: true }).check()
    await publish.getByRole('button', { name: 'Publish translations', exact: true }).click()
  }
  else await reconcilePublishedArticle(editor, publish)
  const link = editor.getByRole('link', { name: 'English', exact: true })
  await link.waitFor()
  assert.equal(await link.getAttribute('href'), publicPath)
  await assertPublishedDraft(request, apiPath, publicPath, title)
  if (type === 'event') {
    const omitted = `/api/v1/content/event/${expectedTitleSlug('warsztaty-spolleczne', id)}?language=pl`
    assert.equal((await request(omitted)).status(), 404)
    await publishOtherTranslation(page, editor, id, request, omitted, apiPath, title)
  }
  if (keepPublished) {
    await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
    return { id, path: publicPath }
  }
  await editor.getByRole('button', { name: 'Unpublish', exact: true }).click()
  const removal = editor.getByRole('region', { name: 'Remove this publication and clear readiness?', exact: true })
  await removal.getByRole('button', { name: 'Confirm', exact: true }).click()
  await editor.getByText('Publication removed.', { exact: true }).waitFor()
  assert.equal((await request(apiPath)).status(), 404)
  assert.equal((await request(publicPath)).status(), 404)
  await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
  await editor.waitFor({ state: 'hidden' })
  assert.equal(await page.getByRole('button', { name: 'Close', exact: true }).count(), 0)
}

async function verifyDraftSurvivesMenuError(page, editor, title) {
  await page.route('**/api/v1/admin/content-types?offset=100', route => route.fulfill({
    status: 503, json: { code: 'security_unavailable' },
  }), { times: 1 })
  const navigation = page.locator('#admin-nav-content')
  await navigation.getByRole('button', { name: 'Load more collections', exact: true }).click()
  await navigation.getByRole('alert').waitFor()
  assert.equal(await editor.getByLabel(/^Title(?: \(|$)/).inputValue(), title)
}

async function interruptPublicationRefresh(page, type, id) {
  await page.route(`**/api/v1/admin/content/${type}/${id}/schema`, route => route.fulfill({
    status: 503, json: { code: 'security_unavailable' },
  }), { times: 1 })
}

async function reconcilePublishedArticle(editor, publish) {
  assert.equal(await publish.count(), 0)
  await editor.getByText('Translations published.', { exact: true }).waitFor()
  await editor.getByText('Reload the draft to view the published links.', { exact: true }).waitFor()
  assert.equal(await editor.getByRole('button', { name: 'Publish translations', exact: true }).isDisabled(), true)
  await editor.getByRole('button', { name: 'Reload draft', exact: true }).click()
}

async function publishOtherTranslation(page, editor, id, request, polishPath, englishPath, oldTitle) {
  const language = editor.getByLabel('Language', { exact: true })
  await language.selectOption('en')
  await editor.getByLabel(/^Title(?: \(|$)/).fill('Unpublished headline')
  const saved = waitForContentMutation(page, `/api/v1/admin/content/event/${id}`, 'PATCH', 200)
  await editor.getByRole('button', { name: 'Save draft', exact: true }).click()
  await editor.getByText('Draft saved.', { exact: true }).waitFor()
  await recordReadyUnpublishedChange(page, editor, id, await saved)
  await language.selectOption('pl')
  await editor.getByRole('button', { name: 'Publish translations', exact: true }).click()
  const publish = editor.getByRole('region', { name: 'Publish the selected translations?', exact: true })
  assert.equal(await publish.getByRole('checkbox', { name: 'Polski', exact: true }).isChecked(), true)
  assert.equal(await publish.getByRole('checkbox', { name: 'English', exact: true }).isChecked(), false)
  await losePublicationResponse(page, id, publish, editor)
  assert.equal(await editor.getByRole('button', { name: 'Publish translations', exact: true }).isDisabled(), true)
  await editor.getByRole('button', { name: 'Reload draft', exact: true }).click()
  await editor.getByRole('link', { name: 'Polski', exact: true }).waitFor()
  const polish = await request(polishPath)
  assert.equal(polish.status(), 200)
  assert.equal((await polish.json()).localized.title.value, 'Warsztaty społeczne')
  const english = await request(englishPath)
  assert.equal(english.status(), 200)
  assert.equal((await english.json()).localized.title.value, oldTitle)
}

async function losePublicationResponse(page, itemID, publish, editor) {
  const session = await page.context().newCDPSession(page)
  const urlPattern = new URL(`/api/v1/admin/content/event/${itemID}/publish`, origin).href
  let serverStatus
  let deliveryError
  session.once('Fetch.requestPaused', ({ requestId, responseStatusCode }) => {
    serverStatus = responseStatusCode
    return session.send('Fetch.failRequest', { requestId, errorReason: 'Failed' }).catch(error => { deliveryError = error })
  })
  try {
    await session.send('Fetch.enable', { patterns: [{ urlPattern, requestStage: 'Response' }] })
    await publish.getByRole('button', { name: 'Publish translations', exact: true }).click()
    const notice = editor.getByText('Publication could not be confirmed. Reload the draft and check the published links before trying again.', { exact: true })
    await notice.waitFor().catch(async (error) => {
      const alerts = await editor.getByRole('alert').allTextContents()
      throw new Error(`Publication response ${serverStatus ?? 'not observed'}, alerts ${JSON.stringify(alerts)}`, { cause: deliveryError ?? error })
    })
    assert.equal(serverStatus, 200, 'Chromium dropped a successful server response')
  }
  finally {
    await session.detach()
  }
}

async function recordReadyUnpublishedChange(page, editor, itemID, saved) {
  // Reuse the live token. Reading /auth/csrf rotates it out from under the open editor.
  const csrf = await saved.request().headerValue('x-csrf-token')
  assert.ok(csrf, 'The saved draft used a live CSRF token')
  const draft = await saved.json()
  const readyPath = `/api/v1/admin/content/event/${encodeURIComponent(itemID)}/ready`
  const result = await page.request.post(new URL(readyPath, origin).href, {
    headers: { origin, 'x-csrf-token': csrf },
    data: { expected_version: draft.version, schema_no: draft.schema_no, locale: 'en' },
    maxRedirects: 0,
  })
  assert.equal(result.status(), 200)
  await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
  await page.getByRole('button', { name: /^Unpublished headline(?: |$)/u }).click()
  await editor.getByText('This translation is ready.', { exact: true }).waitFor()
}

async function verifyIncompletePublication(editor, save, body) {
  await save.click()
  await editor.getByText('Draft saved.', { exact: true }).waitFor()
  await editor.getByRole('button', { name: 'Publish translations', exact: true }).click()
  const failure = editor.getByRole('alert')
  await failure.waitFor()
  assert.equal(await failure.textContent(), 'Complete the highlighted fields before marking the draft ready.')
  assert.equal(await body.getAttribute('aria-invalid'), 'true')
  assert.ok(await body.evaluate(element => element === document.activeElement))
  assert.equal(await editor.getByRole('link', { name: 'English', exact: true }).count(), 0)
}

async function assertPublishedDraft(request, apiPath, publicPath, title) {
  const published = await request(apiPath)
  assert.equal(published.status(), 200)
  const body = await published.json()
  assert.equal(body.localized.title.value, title)
  assert.equal(Object.hasOwn(body.localized, 'subtitle'), false)
  const rendered = await request(publicPath)
  assert.equal(rendered.status(), 200)
  assert.ok((await rendered.text()).includes(title))
}

async function addSecondTranslation(page, editor, id) {
  await editor.getByLabel('Language', { exact: true }).selectOption('pl')
  await editor.getByLabel(/^Title(?: \(|$)/).fill('Warsztaty społeczne')
  const save = editor.getByRole('button', { name: 'Save draft', exact: true })
  await assertSavedAddress(page, 'event', id, save, 'pl', expectedTitleSlug('warsztaty-spolleczne', id))
  await editor.getByText('Draft saved.', { exact: true }).waitFor()
}

async function verifyCollectionAccessChallenge(page, initial, create) {
  const challenge = () => openIdentityChallenge(page, '**/api/v1/admin/content-types', create)
  const { dialog } = await challenge()
  await dialog.getByLabel('Password', { exact: true }).fill('Cancel this confirmation')
  await dialog.getByLabel('Verification code', { exact: true }).fill('123456')
  await page.keyboard.press('Escape')
  await dialog.waitFor({ state: 'hidden' })
  assert.equal(await page.locator('#admin-step-up-password').count(), 0)
  assert.ok(await create.evaluate(element => element === document.activeElement))
  const { csrf } = await challenge()
  assert.equal(await dialog.getByLabel('Password', { exact: true }).inputValue(), '')
  assert.equal(await dialog.getByLabel('Verification code', { exact: true }).inputValue(), '')
  await confirmIdentityInBrowser(page, dialog, csrf)
  assert.ok(await initial.isChecked())
  assert.ok(await create.isVisible())
}

async function assertEditorLayouts(page, editor) {
  const verifyViewport = async (width, height) => {
    await page.setViewportSize({ width, height })
    const verifyTheme = async value => {
      await chooseTheme(page, value)
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

async function selectInterfaceLanguage(page, code) {
  const switcher = page.getByTestId('language-switcher')
  const summary = switcher.locator('summary')
  await summary.press('Enter')
  await switcher.locator(`button[data-language="${code}"]`).click()
  await page.waitForFunction(language => document.documentElement.lang === language, code)
  assert.equal(await summary.getAttribute('data-current-language'), code)
  assert.ok(await summary.evaluate(element => element === document.activeElement))
}

async function switchLanguageWithUnsavedDraft(page, title, expected) {
  await selectInterfaceLanguage(page, 'en')
  const englishEditor = page.getByRole('region', { name: 'Draft editor', exact: true })
  assert.equal(await englishEditor.getByLabel('Language', { exact: true }).inputValue(), 'pl')
  assert.equal(await englishEditor.getByLabel(/^Title/u).inputValue(), expected)
  await selectInterfaceLanguage(page, 'pl')
  assert.equal(await title.inputValue(), expected)
}

async function assertTitleAddresses(page, editor, locale, title, id, original) {
  const save = editor.getByRole('button', { name: 'Zapisz szkic', exact: true })
  const samples = [
    ['ru', 'Весенний праздник', expectedTitleSlug('vesenniy-prazdnik', id)],
    ['kk', 'Қазақша сөз', expectedTitleSlug('qazaqsha-soz', id)],
    ['pl', '🌿', `item-${id}`],
    ['pl', original, expectedTitleSlug('spacer-po-allmaty', id)],
  ]
  assert.equal(await editor.getByLabel(/^Adres URL/u).count(), 0)
  await eachInOrder(samples, async ([code, text, slug]) => {
    await locale.selectOption(code)
    await title.fill(text)
    await assertSavedAddress(page, 'article', id, save, code, slug)
    await editor.getByRole('status').getByText('Zapisano szkic.').waitFor()
  })
}

async function verifyEditorialNavigation(page) {
  await selectInterfaceLanguage(page, 'pl')
  await openSection(page, 'Materiały')
  await collectionButton(page, 'content', 'Artykuł').click()
  const creating = waitForContentMutation(page, '/api/v1/admin/content/article', 'POST', 201)
  await page.getByRole('button', { name: 'Utwórz szkic', exact: true }).click()
  const { item_id: id } = await (await creating).json()
  const editor = page.getByRole('region', { name: 'Edytor szkicu', exact: true })
  const locale = editor.getByLabel('Język', { exact: true })
  const title = editor.getByLabel(/^Tytuł/u)
  const original = 'Spacer po Ałmaty'
  const updated = 'Jesienny spacer po Ałmaty'
  await locale.waitFor()
  await assertTitleAddresses(page, editor, locale, title, id, original)
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
  await switchLanguageWithUnsavedDraft(page, title, updated)
  const settings = page.getByRole('button', { name: 'Ustawienia portalu', exact: true })
  await settings.click()
  const leave = page.getByRole('dialog')
  await leave.getByRole('button', { name: 'Wróć do edycji', exact: true }).click()
  assert.equal(await title.inputValue(), updated)
  assert.ok(await settings.evaluate(element => element === document.activeElement))
  const save = editor.getByRole('button', { name: 'Zapisz szkic', exact: true })
  await assertSavedAddress(page, 'article', id, save, 'pl', expectedTitleSlug('jesienny-spacer-po-allmaty', id))
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
  await page.getByRole('heading', { name: 'Informacje o portalu', level: 1, exact: true }).waitFor()
  await selectInterfaceLanguage(page, 'en')
  await heading(page, 'Portal details').waitFor()
}

async function assertPublishedLocales(page) {
  await heading(page, 'Sign in').waitFor()
  await page.waitForFunction(() => document.documentElement.lang === 'en')
  const language = page.getByTestId('language-switcher')
  const summary = language.locator('summary')
  const { language_flags: flags } = await readPublicSite(page.context())
  assert.equal(new Set(Object.values(flags)).size, 4)
  await waitForFlag(summary, flags.en)
  await summary.press('Enter')
  assert.deepEqual(await language.locator('button[data-language]').evaluateAll(options => options.map(option => option.dataset.language)), ['en', 'kk', 'pl', 'ru'])
  await waitForFlag(language.locator('button[data-language="kk"]'), flags.kk)
  await language.locator('button[data-language="kk"]').press('Escape')
  assert.equal(await language.getAttribute('open'), null)
  assert.ok(await summary.evaluate(element => element === document.activeElement))
  await eachInOrder([['kk', 'Кіру'], ['ru', 'Вход'], ['pl', 'Zaloguj się']], async ([locale, name]) => {
    await selectInterfaceLanguage(page, locale)
    await heading(page, name).waitFor()
    await waitForFlag(summary, flags[locale])
  })
  const localized = await page.reload()
  assert.equal(localized.status(), 200)
  assert.match(await localized.text(), /<html[^>]+lang="pl"/u)
  await heading(page, 'Zaloguj się').waitFor()
  assert.equal(await summary.getAttribute('data-current-language'), 'pl')
  await waitForFlag(summary, flags.pl)
  await page.goto(origin)
  await page.waitForURL(`${origin}/pl/articles`)
  assert.equal(await summary.getAttribute('data-current-language'), 'pl')
  await waitForFlag(summary, flags.pl)
  await summary.press('Enter')
  const russian = language.locator('a[data-language="ru"]')
  await waitForFlag(russian, flags.ru)
  await russian.click()
  await page.waitForURL(`${origin}/ru/articles`)
  await waitForFlag(summary, flags.ru)
  await page.goto(admin)
  await heading(page, 'Вход').waitFor()
  await selectInterfaceLanguage(page, 'en')
  await heading(page, 'Sign in').waitFor()
}

async function verifyWorkspaceGeometry(page) {
  const breakpoints = [[1440, 900], [768, 1024], [390, 844], [320, 844]]
  const workspaceTop = () => page.locator('.admin-workspace').evaluate(element =>
    element.getBoundingClientRect().top + window.scrollY)
  await eachInOrder(breakpoints, async ([width, height]) => {
    await page.setViewportSize({ width, height })
    await eachInOrder(['light', 'dark'], async (theme) => {
      await chooseTheme(page, theme)
      await assertHeaderIcons(page)
      const initialHeader = await headerGeometry(page)
      const initialTop = await workspaceTop()
      await eachInOrder(['kk', 'pl', 'ru', 'en'], async (locale) => {
        await selectInterfaceLanguage(page, locale)
        assertStablePreferences(initialHeader, await headerGeometry(page))
        assert.ok(Math.abs(await workspaceTop() - initialTop) <= 1, 'A shipped language moved the workspace')
        await assertFit(page)
        await assertSidebarChevrons(page)
      })
    })
  })
  await chooseTheme(page, 'system')
  await page.setViewportSize({ width: 768, height: 1024 })
}

async function verifyCompactNavigation(page) {
  await page.setViewportSize({ width: 320, height: 844 })
  const rail = page.locator('.admin-nav-rail')
  await rail.waitFor()
  const controls = await rail.getByRole('button').evaluateAll(buttons =>
    buttons.map(button => {
      const { width, height, top } = button.getBoundingClientRect()
      return { width, height, top, icon: Boolean(button.querySelector('svg[aria-hidden="true"]')) }
    }),
  )
  assert.equal(controls.length, 5)
  for (const control of controls) {
    assert.ok(control.width >= 44 && control.height >= 44 && control.icon)
    assert.ok(Math.abs(control.top - controls[0].top) <= 1)
  }
  const content = rail.getByRole('button', { name: 'Content', exact: true })
  assert.equal(await content.getAttribute('title'), 'Content')
  await content.press('Enter')
  const article = collectionButton(page, 'content', 'Article')
  await article.waitFor()
  assert.equal(await content.getAttribute('aria-expanded'), 'true')
  assert.equal(await article.locator('svg.admin-icon').count(), 1)
  await article.press('Escape')
  assert.equal(await content.getAttribute('aria-expanded'), 'false')
  assert.ok(await content.evaluate(button => button === document.activeElement))

  const names = page.getByRole('button', { name: 'Section names', exact: true })
  await names.click()
  await rail.waitFor({ state: 'hidden' })
  assert.equal(await names.getAttribute('aria-pressed'), 'true')
  await page.locator('.admin-nav-parent').getByText('Content', { exact: true }).waitFor()
  await assertFit(page)
  await names.click()
  await rail.waitFor()
  await page.setViewportSize({ width: 768, height: 1024 })
}

async function assertSidebarChevrons(page) {
  const contained = await page.locator('.admin-nav-chevron').evaluateAll(chevrons =>
    chevrons.every((chevron) => {
      const button = chevron.parentElement.getBoundingClientRect()
      const icon = chevron.getBoundingClientRect()
      return icon.left >= button.left && icon.right <= button.right
    }),
  )
  assert.ok(contained, 'Disclosure chevrons must stay inside their buttons')
}

async function assertHeaderIcons(page) {
  const utilities = page.locator('.admin-preferences')
  const boxes = await utilities.locator(':scope > button, .header-preference-trigger').evaluateAll(elements =>
    elements.map(element => {
      const { width, height, top, left, right } = element.getBoundingClientRect()
      const decorativeIcon = element.querySelector('svg[aria-hidden="true"], .language-flag[aria-hidden="true"] img[alt=""]')
      return { width, height, top, left, right, hasIcon: Boolean(decorativeIcon) }
    }))
  assert.equal(boxes.length, 4)
  const viewportWidth = await page.evaluate(() => innerWidth)
  for (const box of boxes) {
    assert.ok(box.width >= 44 && box.height >= 44)
    assert.ok(box.hasIcon)
    assert.ok(Math.abs(box.top - boxes[0].top) <= 1)
    assert.ok(box.left >= 0 && box.right <= viewportWidth)
  }
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
  const negotiated = await context.request.get(admin, { headers: { 'Accept-Language': 'kk-KZ, en;q=0.5' } })
  assert.match(await negotiated.text(), /<html[^>]+lang="kk"/u)
  await assertPublishedLocales(page)
  const cookie = await signIn(page, context)
  await verifyWorkspaceGeometry(page)
  await verifyCompactNavigation(page)
  await verifySessionPolicy(page)
  await verifyNestedNavigation(page)
  await verifyTextSearchAfterResize(page)
  await verifySettingsSave(page, context)
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
