import assert from 'node:assert/strict'
import { assertEditorMutationBlocksNavigation, assertSavedAddress, eachInOrder, waitForContentMutation } from './admin-access.mjs'
import { assertFit, assertTheme, collectionButton, heading, openSection, origin, selectInterfaceLanguage } from './admin-browser-helpers.mjs'
import { chooseTheme } from './header-preferences.mjs'

export async function createContentDraftWithKeyboard(page, type, label) {
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

export async function verifyContentWorkflow(page, context, { type, namespace, label, slugBase: slug, title, checkLayout = false, keepPublished = false }) {
  const { item_id: id } = await createContentDraftWithKeyboard(page, type, label)
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
    const omitted = '/api/v1/content/event/warsztaty-spolleczne?language=pl'
    assert.equal((await request(omitted)).status(), 404)
    await publishOtherTranslation(page, editor, id, request, omitted, apiPath, title)
  }
  if (keepPublished) {
    await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
    return { id, path: publicPath }
  }
  await editor.getByRole('button', { name: 'Unpublish', exact: true }).click()
  if (type === 'event') await verifyUnconfirmedRemoval(page, editor, id)
  else {
    await confirmRemoval(editor)
    await editor.getByText('Publication removed.', { exact: true }).waitFor()
    await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
  }
  assert.equal((await request(apiPath)).status(), 404)
  assert.equal((await request(publicPath)).status(), 404)
  await editor.waitFor({ state: 'hidden' })
  assert.equal(await page.getByRole('button', { name: 'Close', exact: true }).count(), 0)
}

function confirmRemoval(editor) {
  const removal = editor.getByRole('region', { name: 'Remove this publication and clear readiness?', exact: true })
  return removal.getByRole('button', { name: 'Confirm', exact: true }).click()
}

async function verifyUnconfirmedRemoval(page, editor, id) {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.route(`**/api/v1/admin/content/event/${id}/unpublish`, async (route) => {
    const committed = await route.fetch()
    assert.equal(committed.status(), 200)
    await route.abort('failed')
  }, { times: 1 })
  await page.route(`**/api/v1/admin/content/event/${id}/schema`, route => route.fulfill({
    status: 503, json: { code: 'security_unavailable' },
  }), { times: 2 })
  await confirmRemoval(editor)
  await editor.getByRole('alert').filter({ hasText: 'Reload the draft to view the published links.' }).waitFor()
  await editor.getByRole('button', { name: 'Reload draft', exact: true }).click()
  await editor.getByRole('alert').filter({ hasText: 'Administration is temporarily unavailable.' }).waitFor()
  assert.equal(await editor.getByText('Published:', { exact: true }).count(), 0)
  assert.equal(await editor.getByRole('link', { name: 'English', exact: true }).count(), 0)
  const row = page.locator(`[data-item-id="${id}"]`).getByRole('button')
  await row.waitFor()
  const status = row.locator('.draft-summary').getByText(/^(?:Published:|Unpublished$)/u)
  assert.equal(await status.count(), 0)
  await page.route('**/api/v1/admin/content/event?offset=*', route => route.fulfill({
    status: 503, json: { code: 'security_unavailable' },
  }), { times: 1 })
  await editor.getByRole('button', { name: 'Back to drafts', exact: true }).click()
  const retry = page.getByRole('button', { name: 'Retry', exact: true })
  await retry.waitFor()
  assert.equal(await status.count(), 0)
  await retry.click()
  await row.getByText('Unpublished', { exact: true }).waitFor()
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
  await assertSavedAddress(page, 'event', id, save, 'pl', 'warsztaty-spolleczne')
  await editor.getByText('Draft saved.', { exact: true }).waitFor()
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
    ['ru', 'Весенний праздник', 'vesenniy-prazdnik'],
    ['kk', 'Қазақша сөз', 'qazaqsha-soz'],
    ['pl', '🌿', 'item'],
    ['pl', original, 'spacer-po-allmaty'],
  ]
  assert.equal(await editor.getByLabel(/^Adres URL/u).count(), 0)
  await eachInOrder(samples, async ([code, text, slug]) => {
    await locale.selectOption(code)
    await title.fill(text)
    await assertSavedAddress(page, 'article', id, save, code, slug)
    await editor.getByRole('status').getByText('Zapisano szkic.').waitFor()
  })
}

export async function verifyEditorialNavigation(page) {
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
  await assertSavedAddress(page, 'article', id, save, 'pl', 'jesienny-spacer-po-allmaty')
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
