import assert from 'node:assert/strict'
import { assertEditorMutationBlocksNavigation, waitForContentMutation } from './admin-access.mjs'
import { assertFit, heading, openSection, origin, readPublicSite, selectInterfaceLanguage, verifyScopedSaveButton, waitForFlag } from './admin-browser-helpers.mjs'

async function readAdminUI(context, audience) {
  const result = await context.request.get(new URL(`/api/v1/admin/ui/${audience}?lang=en`, origin).href, {
    headers: { Origin: origin },
  })
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

async function verifyPortalTitleSave(page, settings, newTitle) {
  await page.getByRole('button', { name: 'Portal settings', exact: true }).click()
  const portalName = page.getByLabel('Portal name', { exact: true })
  assert.equal(await portalName.inputValue(), settings.title)
  const save = page.getByRole('button', { name: 'Save changes', exact: true })
  await verifyScopedSaveButton(save, '#portal-title')
  assert.equal(await save.isDisabled(), true)
  assert.equal(await page.getByRole('button', { name: 'Close', exact: true }).count(), 0)
  await page.setViewportSize({ width: 320, height: 844 })
  await assertFit(page)
  await portalName.fill(newTitle)
  await page.getByRole('button', { name: 'Appearance', exact: true }).click()
  assert.ok(await page.getByLabel('Choose accent color', { exact: true }).isVisible())
  assert.equal(await portalName.count(), 0)
  await page.getByRole('button', { name: 'Portal details', exact: true }).click()
  assert.equal(await portalName.inputValue(), newTitle)
  await verifyConcurrentDetailsUpdate(page, settings, save)
}

async function verifyConcurrentDetailsUpdate(page, settings, save) {
  const routePath = '**/api/v1/admin/settings/details'
  const description = 'Community news and events'
  await page.route(routePath, async (route) => {
    const sent = route.request().postDataJSON()
    const concurrent = await route.fetch({
      postData: { ...sent, details: { ...sent.details, title: settings.title, description } },
    })
    assert.equal(concurrent.status(), 200)
    await route.continue()
  }, { times: 1 })
  const rejected = waitForContentMutation(page, '/api/v1/admin/settings/details', 'POST', 409)
  await save.click()
  await rejected
  const reload = page.getByRole('button', { name: 'Load saved version and keep my edits', exact: true })
  await reload.and(page.locator(':focus')).waitFor()
  await reload.click()
  await reload.waitFor({ state: 'hidden' })
  assert.equal(await page.getByLabel('Description', { exact: true }).inputValue(), description)
  const details = waitForContentMutation(page, '/api/v1/admin/settings/details', 'POST', 200)
  await save.click()
  const changedDetails = await details
  assert.deepEqual(Object.keys(changedDetails.request().postDataJSON()), ['expected_version', 'details'])
  assert.equal(changedDetails.request().postDataJSON().details.description, description)
  await page.getByTestId('scoped-settings-status').waitFor()
}

export async function verifySettingsSave(page, context) {
  const oldSite = await readPublicSite(context)
  const oldEntry = await readAdminUI(context, 'entry')
  const newTitle = 'Polacy w Kazachstanie'
  const newMessage = 'Enter your code to open the community portal.'
  await verifyPortalTitleSave(page, oldSite.settings, newTitle)
  const beforeTranslation = await readPublicSite(context)
  assert.equal(beforeTranslation.settings.title, newTitle)
  assert.deepEqual(beforeTranslation.messages, oldSite.messages)
  const message = await findSettingsTranslation(page, oldEntry.messages['admin.code_help'])
  const saveTexts = page.getByRole('button', { name: 'Save translations', exact: true })
  await verifyScopedSaveButton(saveTexts, '#portal-message-value')
  await message.fill('   ')
  assert.equal(await saveTexts.count(), 1)
  await saveTexts.click()
  await reopenMissingText(page, oldEntry.messages['admin.code_help'])
  assert.deepEqual(await readPublicSite(context), beforeTranslation)

  await message.fill(newMessage)
  const translation = waitForContentMutation(page, '/api/v1/admin/settings/catalogs', 'POST', 200)
  await assertEditorMutationBlocksNavigation(page, '**/api/v1/admin/settings/catalogs', 'POST', () => saveTexts.click(), ['Content', 'Sign out'])
  const payload = (await translation).request().postDataJSON()
  assert.equal(payload.language_code, 'en')
  assert.deepEqual(payload.messages, { 'admin.code_help': newMessage })
  await page.getByTestId('scoped-settings-status').waitFor()
  assert.equal(await saveTexts.isDisabled(), true)
  await page.locator('.admin-name').getByText(newTitle, { exact: false }).waitFor()
  const published = await readPublicSite(context)
  assert.notEqual(published.version, oldSite.version)
  assert.equal(published.settings.title, newTitle)
  assert.deepEqual(published.messages, oldSite.messages)
  const entry = await readAdminUI(context, 'entry')
  assert.equal(entry.version, published.version)
  assert.equal(entry.messages['admin.code_help'], newMessage)
  const document = await page.reload()
  assert.equal(document.status(), 200)
  assert.match(await document.text(), /Polacy w Kazachstanie/u)
  await heading(page, 'Interface texts').waitFor()
  assert.equal(new URL(page.url()).searchParams.get('view'), 'translations')
}

export async function verifyCatalogConflictAfterDirectorySave(page, context) {
  const key = 'admin.code_help'
  const pending = 'Enter the verification code from your app.'
  const concurrent = 'Use the code displayed by your authenticator.'
  const languages = '/api/v1/admin/settings/languages'
  const catalogs = '/api/v1/admin/settings/catalogs'
  const settings = await context.request.get(`${origin}/api/v1/admin/settings`, { headers: { Origin: origin } })
  assert.equal(settings.status(), 200)
  const version = (await settings.json()).catalog_versions.en
  await page.getByRole('searchbox', { name: 'Search texts', exact: true }).fill(key)
  await page.getByRole('list', { name: 'Text to edit', exact: true }).getByRole('button').click()
  const translation = page.getByLabel('Translation', { exact: true })
  await translation.fill(pending)

  await page.getByRole('button', { name: 'Languages', exact: true }).click()
  await page.getByRole('button', { name: 'Add language', exact: true }).click()
  await page.getByLabel('Language code (BCP 47)', { exact: true }).fill('de')
  await page.getByLabel('Language name', { exact: true }).fill('German')
  await page.getByLabel('Name in the language itself', { exact: true }).fill('Deutsch')
  await page.route(`**${languages}`, async (route) => {
    const csrf = await route.request().headerValue('x-csrf-token')
    const response = await context.request.post(origin + catalogs, {
      headers: { Origin: origin, 'X-CSRF-Token': csrf },
      data: { expected_version: version, language_code: 'en', messages: { [key]: concurrent } },
    })
    assert.equal(response.status(), 200)
    await route.continue()
  }, { times: 1 })
  const directorySaved = waitForContentMutation(page, languages, 'POST', 200)
  const directoryRefresh = page.waitForResponse(response =>
    new URL(response.url()).pathname === '/api/v1/admin/settings' && response.request().method() === 'GET' && response.ok())
  await page.getByRole('button', { name: 'Save changes', exact: true }).click()
  await directorySaved
  await directoryRefresh
  await page.getByRole('button', { name: 'Interface texts', exact: true }).click()
  assert.equal(await translation.inputValue(), pending)
  const save = page.getByRole('button', { name: 'Save translations', exact: true })
  const result = page.waitForResponse(response => new URL(response.url()).pathname === catalogs && response.request().method() === 'POST')
  await save.click()
  assert.equal((await result).status(), 409)
  assert.equal((await readAdminUI(context, 'entry')).messages[key], concurrent)
  const reload = page.getByRole('button', { name: 'Load saved version and keep my edits', exact: true })
  await reload.click()
  await reload.waitFor({ state: 'hidden' })
  assert.equal(await translation.inputValue(), pending)
  const saved = waitForContentMutation(page, catalogs, 'POST', 200)
  await save.click()
  await saved
  assert.equal((await readAdminUI(context, 'entry')).messages[key], pending)
}

export async function verifySlugRuleSettings(page, context) {
  const snapshot = await readAdminUI(context, 'catalog')
  const polish = snapshot.slug_rules.filter(rule => rule.language_code === 'pl')
  assert.deepEqual(Object.fromEntries(polish.map(rule => [rule.character, rule.replacement])),
    { ą: 'a', ć: 'c', ę: 'e', ł: 'l', ń: 'n', ó: 'o', ś: 's', ź: 'z', ż: 'z' })
  await openSection(page, 'Languages and translations')
  await page.getByRole('button', { name: 'Languages', exact: true }).click()
  await verifyLocalePreferences(page, context, snapshot.default_language)
  const language = page.getByLabel('Rule language', { exact: true })
  const search = page.getByRole('searchbox', { name: 'Search rules', exact: true })
  const replacement = page.getByLabel('Replacement', { exact: true })
  const save = page.getByRole('button', { name: 'Save address rules', exact: true })
  await verifyScopedSaveButton(save, '#portal-slug-language')
  await language.selectOption('pl')
  await search.fill('ł')
  assert.equal(await replacement.inputValue(), 'l')
  assert.equal(await save.isDisabled(), true)
  await replacement.fill('ll')
  const publishedReplacement = async () => (await readAdminUI(context, 'catalog')).slug_rules.find(rule =>
    rule.language_code === 'pl' && rule.character === 'ł')?.replacement
  assert.equal(await publishedReplacement(), 'l')
  const delta = waitForContentMutation(page, '/api/v1/admin/settings/slug-rules', 'POST', 200)
  await save.click()
  const request = (await delta).request().postDataJSON()
  assert.deepEqual(Object.keys(request), ['expected_version', 'upsert'])
  assert.deepEqual(request.upsert, [{ language_code: 'pl', character: 'ł', replacement: 'll' }])
  const status = page.getByTestId('scoped-settings-status')
  await status.waitFor()
  const bounds = await status.boundingBox()
  assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= page.viewportSize().height)
  assert.equal(await publishedReplacement(), 'll')
}

async function verifyLocalePreferences(page, context, originalLanguage) {
  const select = page.locator('#portal-default-language')
  const save = page.getByRole('button', { name: 'Save language preferences', exact: true })
  const other = originalLanguage === 'pl' ? 'en' : 'pl'
  await select.selectOption(other)
  const path = '/api/v1/admin/settings/locale-defaults'
  await page.route(`**${path}`, route => route.fulfill({ status: 400, json: { code: 'invalid_locale_defaults' } }), { times: 1 })
  await save.click()
  await page.getByRole('alert').filter({ hasText: 'Choose an enabled content language and an available interface translation.' }).waitFor()
  await select.and(page.locator(':focus')).waitFor()
  assert.equal(await select.inputValue(), other)
  await verifySettingsCooldown(page, path, save)
  const changed = waitForContentMutation(page, path, 'POST', 200)
  await save.click()
  assert.deepEqual(Object.keys((await changed).request().postDataJSON()), ['expected_version', 'defaults'])
  assert.equal((await readPublicSite(context)).settings.default_language, other)
  await select.selectOption(originalLanguage)
  const restored = waitForContentMutation(page, path, 'POST', 200)
  await save.click()
  await restored
}

async function verifySettingsCooldown(page, path, save) {
  await page.route(`**${path}`, route => route.fulfill({
    status: 429, headers: { 'Retry-After': '2' }, json: { code: 'rate_limited' },
  }), { times: 1 })
  await save.click()
  await page.getByRole('alert').filter({ hasText: 'Too many attempts.' }).waitFor()
  assert.equal(await save.isDisabled(), true)
  await save.and(page.locator(':enabled')).waitFor()
}

async function openAppearance(page) {
  const sidebar = page.locator('.admin-navigation')
  await sidebar.getByRole('button', { name: 'Portal settings', exact: true }).click()
  await sidebar.getByRole('button', { name: 'Appearance', exact: true }).click()
}

async function changeAppearanceDraft(page) {
  await openAppearance(page)
  const accent = page.locator('#portal-accent')
  const original = await accent.inputValue()
  const changed = '#774411'
  await accent.fill(changed)
  await page.locator('.admin-navigation').getByRole('button', { name: 'Languages and translations', exact: true }).click()
  return { original, changed }
}

async function verifyIndependentAppearanceSave(page, context, appearance, flag) {
  assert.equal((await readPublicSite(context)).settings.accent, appearance.original)
  await openAppearance(page)
  const accent = page.locator('#portal-accent')
  assert.equal(await accent.inputValue(), appearance.changed)
  const save = page.getByRole('button', { name: 'Save appearance', exact: true })
  const saved = page.getByTestId('scoped-settings-status')
  await save.click()
  await saved.waitFor()
  const published = await readPublicSite(context)
  assert.equal(published.settings.accent, appearance.changed)
  assert.equal(published.language_flags.en, flag)
  await accent.fill(appearance.original)
  await save.click()
  await saved.waitFor()
  assert.equal((await readPublicSite(context)).settings.accent, appearance.original)
  await page.locator('.admin-navigation').getByRole('button', { name: 'Languages and translations', exact: true }).click()
}

async function verifyPublicFlagReset(page, context, preview, trigger, saveFlag, flag) {
  const publicPage = await context.newPage()
  try {
    await publicPage.goto(`${origin}/en/articles`)
    const publicTrigger = publicPage.getByTestId('language-switcher').locator('summary')
    await waitForFlag(publicTrigger, flag)

    await page.getByRole('button', { name: 'Use globe', exact: true }).click()
    await waitForFlag(preview)
    await waitForFlag(trigger, flag)
    await saveFlag()
    assert.equal((await readPublicSite(context)).language_flags.en, undefined)
    await waitForFlag(trigger)
    await publicPage.reload()
    await waitForFlag(publicTrigger)
  }
  finally {
    await publicPage.close()
  }
}

export async function verifyLanguageFlagSettings(page, context) {
  const source = page.getByRole('textbox', { name: 'Flag SVG', exact: true })
  const save = page.getByRole('button', { name: 'Save flag', exact: true })
  const saved = page.getByTestId('scoped-settings-status')
  const saveFlag = async () => {
    const response = page.waitForResponse(result =>
      new URL(result.url()).pathname.endsWith('/settings/language-flags') && result.request().method() === 'POST')
    await save.click()
    assert.equal((await response).status(), 200)
    await saved.waitFor()
    assert.equal(await saved.textContent(), 'Settings saved.')
  }
  assert.match(await page.locator('#portal-flag-help').textContent(), /viewBox="0 0 30 20"\s*·\s*24 × 16 px/u)
  const trigger = page.getByTestId('language-switcher').locator('summary')
  const original = await source.inputValue()
  const invalid = '<svg viewBox="0 0 24 16"><path d="M0 0 L"/></svg>'
  await source.fill(invalid)
  await save.click()
  await page.getByRole('alert').getByText('This flag could not be saved. Check the SVG and try again.').waitFor()
  assert.equal(await source.inputValue(), invalid)
  assert.ok(await source.evaluate(element => element === document.activeElement))
  await waitForFlag(trigger, original)

  await source.fill('<svg viewBox="0 0 24 16"><!-- Flag background --><rect width="24" height="16" fill="#6a158c"/></svg>')
  const preview = page.getByTestId('language-flag-preview')
  await preview.locator('img[src*="%236a158c"]').waitFor()
  await waitForFlag(trigger, original)
  const appearance = await changeAppearanceDraft(page)
  await preview.locator('img[src*="%236a158c"]').waitFor()
  await page.locator('#portal-flag-language').selectOption('pl')
  const published = await readPublicSite(context)
  await waitForFlag(preview, published.language_flags.pl)
  await page.locator('#portal-flag-language').selectOption('en')
  await preview.locator('img[src*="%236a158c"]').waitFor()
  await saveFlag()
  const { language_flags: flags } = await readPublicSite(context)
  assert.match(flags.en, /#6a158c/u)
  assert.doesNotMatch(flags.en, /<!--/u)
  await waitForFlag(trigger, flags.en)
  await waitForFlag(page.getByTestId('language-flag-preview'), flags.en)
  await verifyIndependentAppearanceSave(page, context, appearance, flags.en)
  await verifyPublicFlagReset(page, context, preview, trigger, saveFlag, flags.en)
  await source.fill(original)
  await saveFlag()
}

export async function verifyTextSearchAfterResize(page) {
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
