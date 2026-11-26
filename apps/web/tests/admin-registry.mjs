import assert from 'node:assert/strict'
import { assertEditorMutationBlocksNavigation, waitForContentMutation } from './admin-access.mjs'
import { assertFit, collectionButton, openSection, origin, verifyScopedSaveButton } from './admin-browser-helpers.mjs'
import { createContentDraftWithKeyboard, verifyContentWorkflow } from './admin-editorial.mjs'

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

async function verifyTranslatedTitleHint(page, field) {
  await field.locator('summary').click()
  await page.locator('#registry-translation-language').selectOption('pl')
  await field.getByPlaceholder('Tytuł', { exact: true }).waitFor()
}

export async function verifyTypeBuilder(page) {
  await openSection(page, 'Structure')
  await collectionButton(page, 'registry', 'Article').click()
  const titleField = page.getByRole('group', { name: 'Title', exact: true })
  await titleField.waitFor()
  assert.equal(await titleField.locator('summary').getByText('title', { exact: true }).count(), 0)
  await verifyTranslatedTitleHint(page, titleField)
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
  await assertEditorMutationBlocksNavigation(page, '**/api/v1/admin/content-types/event/fields', 'POST', () => save.click(), ['Content', 'Sign out'])
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

export async function verifyCustomCollection(page, context) {
  const name = 'Community notices'
  const svg = '<svg xmlns="http://w3.org" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="3"/></svg>'
  const created = await createNamedCollection(page, name, async () => {
    await page.getByLabel('Collection icon (SVG)', { exact: true }).fill(svg)
  })
  const structureIcon = collectionButton(page, 'registry', name).locator('.admin-icon--custom')
  await structureIcon.waitFor()
  const mask = await structureIcon.evaluate(icon => getComputedStyle(icon).maskImage)
  assert.match(mask, /^url\("data:image\/svg\+xml/iu)
  assert.match(decodeURIComponent(mask), /xmlns="http:\/\/www\.w3\.org\/2000\/svg"/u)
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
  assert.match(await page.locator('#registry-icon-help').textContent(), /viewBox="0 0 24 24"\s*·\s*20 × 20 px/u)
  const save = page.getByRole('button', { name: 'Save icon', exact: true })
  await verifyScopedSaveButton(save, '#registry-collection-icon')
  const path = `/api/v1/admin/content-types/${collection.type_key}/icon`
  const wrongNamespace = '<svg xmlns="urn:example:svg" viewBox="0 0 24 24"><rect x="4" y="2" width="16" height="20"/></svg>'
  await input.fill(wrongNamespace)
  await save.click()
  await page.getByRole('alert').filter({ hasText: 'xmlns="http://www.w3.org/2000/svg"' }).waitFor()
  assert.equal(await input.inputValue(), wrongNamespace)
  assert.ok(await save.isDisabled())

  const unsupported = '<svg viewBox="0 0 24 24"><title>Sample</title><circle cx="12" cy="12" r="8"/></svg>'
  await input.fill(unsupported)
  const rejected = waitForContentMutation(page, path, 'POST', 400)
  await save.click()
  assert.equal((await (await rejected).json()).code, 'invalid_collection_icon')
  await page.getByRole('alert').filter({ hasText: 'Use a valid SVG with a viewBox, simple shapes, and up to 4 KB of code.' }).waitFor()
  assert.equal(await input.inputValue(), unsupported)
  const icon = collectionButton(page, 'registry', name).locator('.admin-icon--custom')
  assert.equal(await icon.evaluate(element => getComputedStyle(element).maskImage), previousMask)

  const calendar = '<svg xmlns="http://w3.org" viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="18" rx="2"/><line x1="8" y1="2" x2="8" y2="6" stroke="currentColor"/></svg>'
  await input.fill(calendar)
  const preview = page.getByTestId('collection-icon-preview')
  await preview.locator(`.admin-icon--custom[style*="${encodeURIComponent('<line x1="8"')}"]`).waitFor()
  await input.fill('<svg')
  const editedCalendar = calendar.replace('x1="8"', 'x1="12"')
  await input.fill(editedCalendar)
  await preview.locator(`.admin-icon--custom[style*="${encodeURIComponent('<line x1="12"')}"]`).waitFor()
  assert.equal(await icon.evaluate(element => getComputedStyle(element).maskImage), previousMask)
  assert.equal(await input.inputValue(), editedCalendar)

  await verifyCollectionIconSaveWithPendingField(page, path, save, collection.schema_no)
  await collectionButton(page, 'registry', name).locator('svg.admin-icon').waitFor()
  await openSection(page, 'Content')
  await collectionButton(page, 'content', name).locator('svg.admin-icon').waitFor()
}

async function verifyParallelIconConflict(page, path, currentLabel) {
  const svg = page.getByLabel('Collection icon (SVG)', { exact: true })
  const local = '<svg viewBox="0 0 24 24"><circle r="5"/></svg>'
  await svg.fill(local)
  const label = page.getByRole('group', { name: currentLabel, exact: true }).getByRole('textbox', { name: 'Field label', exact: true })
  await label.fill('Community title')
  await page.route(`**${path}`, async (route) => {
    const response = await route.fetch()
    const body = await response.json()
    await route.fulfill({
      response,
      json: {
        ...body,
        icon_svg: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle r="7"></circle></svg>',
        icon_version: 'a'.repeat(64),
      },
    })
  }, { times: 1 })
  await page.getByRole('button', { name: 'Save collection changes', exact: true }).click()
  const reload = page.getByRole('button', { name: 'Load saved version and keep my edits', exact: true })
  await reload.waitFor()
  assert.equal(await svg.inputValue(), local)
  assert.equal(await page.getByRole('button', { name: 'Save icon', exact: true }).isDisabled(), true)
  await reload.click()
  await reload.waitFor({ state: 'hidden' })
  assert.equal(await svg.inputValue(), local)
  await page.getByRole('button', { name: 'Use default icon', exact: true }).click()
}

async function verifyCollectionIconSaveWithPendingField(page, path, save, schemaNo) {
  const title = page.getByRole('group', { name: 'Title', exact: true })
  await title.locator('summary').click()
  const label = title.getByRole('textbox', { name: 'Field label', exact: true })
  const newLabel = 'Notice title'
  await label.fill(newLabel)
  await page.getByRole('button', { name: 'Use default icon', exact: true }).click()
  await page.route(`**${path}`, route => route.fulfill({
    status: 429, headers: { 'Retry-After': '2' }, json: { code: 'rate_limited' },
  }), { times: 1 })
  await save.click()
  await page.getByRole('alert').filter({ hasText: 'Too many attempts.' }).waitFor()
  assert.ok(await save.isDisabled())
  await save.and(page.locator(':enabled')).waitFor()
  const reset = waitForContentMutation(page, path, 'POST', 200)
  await save.click()
  const result = await (await reset).json()
  assert.equal(result.icon_svg, '')
  assert.match(result.icon_version, /^[0-9a-f]{64}$/u)
  assert.equal((await page.locator('.builder-schema h3').textContent()).trim(), `Current schema ${schemaNo}`)
  assert.equal(await page.getByRole('group', { name: newLabel, exact: true }).getByRole('textbox', { name: 'Field label', exact: true }).inputValue(), newLabel)
  const fieldsPath = path.replace(/\/icon$/u, '/fields')
  const fields = waitForContentMutation(page, fieldsPath, 'POST', 200)
  await page.getByRole('button', { name: 'Save collection changes', exact: true }).click()
  const response = await fields
  assert.deepEqual(Object.keys(response.request().postDataJSON()), ['labels'])
  const saved = await response.json()
  assert.equal(saved.icon_svg, '')
  assert.equal(saved.schema_no, schemaNo)
  assert.ok(saved.fields.some(field => field.display_labels?.en?.label === newLabel))
  await verifyParallelIconConflict(page, fieldsPath, newLabel)
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
