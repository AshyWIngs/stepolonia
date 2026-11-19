import assert from 'node:assert/strict'
import { createHmac, randomBytes } from 'node:crypto'
import { chooseTheme } from './header-preferences.mjs'

const base32Symbols = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
const passphrase = randomBytes(24).toString('base64url')
const button = (page, name) => page.getByRole('button', { name, exact: true })
const heading = (page, name) => page.getByRole('heading', { name, exact: true })

function totp(secret, offset = 0) {
  const bits = [...secret].map(char => base32Symbols.indexOf(char).toString(2).padStart(5, '0')).join('')
  const bytes = Buffer.from(bits.match(/.{8}/gu).map(byte => Number.parseInt(byte, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000) + offset))
  const hash = createHmac('sha1', bytes).update(counter).digest()
  const start = hash.at(-1) & 15
  return ((hash.readUInt32BE(start) & 0x7fffffff) % 1_000_000).toString().padStart(6, '0')
}

async function pageResponse(page, path, status, perform) {
  const response = page.waitForResponse(candidate => new URL(candidate.url()).pathname === path && candidate.status() === status)
  await perform()
  return response
}

export function waitForContentMutation(page, path, method, status) {
  return page.waitForResponse(response => response.request().method() === method &&
    new URL(response.url()).pathname === path && response.status() === status)
}

export function expectedTitleSlug(base, id) {
  return `${base}-${id.replaceAll('-', '')}`
}

export async function assertSavedAddress(page, type, id, save, locale, slug) {
  const saving = waitForContentMutation(page, `/api/v1/admin/content/${type}/${id}`, 'PATCH', 200)
  await save.click()
  const draft = await (await saving).json()
  assert.equal(draft.item_id, id)
  assert.equal(draft.locales[locale].values.slug, slug)
}

export function eachInOrder(values, action) {
  return values.reduce((previous, value) => previous.then(() => action(value)), Promise.resolve())
}

const roleActions = {
  'content.items.create': 'Create items',
  'content.items.read': 'Read items',
  'content.items.update': 'Edit items',
  'content.items.ready': 'Mark items ready',
  'content.items.publish': 'Publish items',
  'content.items.unpublish': 'Unpublish items',
}
const roleScopes = { type: 'Collection', assigned: 'Assigned items', any: 'All items' }
const roleCollections = { article: 'Article', event: 'Event' }
const roleFields = { title: 'Title', body: 'Body', slug: 'URL slug' }

async function stageRole(page, name, grants) {
  await button(page, 'Create role').click()
  await page.getByLabel('Role name', { exact: true }).fill(name)
  await page.getByLabel('Permission area', { exact: true }).selectOption({ label: 'Content' })
  assert.equal(await page.locator('.role-rules').getByText('content.items.read', { exact: true }).count(), 0)
  await eachInOrder(grants, async ([operation, type, scope, fields]) => {
    await page.getByRole('checkbox', { name: `${roleActions[operation]} · ${roleCollections[type]} · ${roleScopes[scope]}`, exact: true }).check()
    await eachInOrder(fields, field => page.getByRole('checkbox', { name: roleFields[field] ?? field, exact: true }).check())
  })
  await button(page, 'Save role').click()
  await heading(page, name).waitFor()
  const [operation, type, scope] = grants[0]
  assert.ok(await page.getByText(`${roleActions[operation]} · ${roleCollections[type]} · ${roleScopes[scope]}`, { exact: true }).isVisible())
  await button(page, 'Enable').click()
  await button(page, 'Verify').click()
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()
}

async function proveIndependentDelegationControls(page) {
  await page.setViewportSize({ width: 390, height: 844 })
  await button(page, 'Create role').click()
  await page.getByLabel('Role name', { exact: true }).fill('Delegation review')
  const save = button(page, 'Save role')
  const saveBox = await save.boundingBox()
  assert.ok(saveBox && saveBox.y >= 0 && saveBox.y + saveBox.height <= 844)
  await page.getByLabel('Permission area', { exact: true }).selectOption({ label: 'All' })
  await page.getByRole('checkbox', { name: 'Delegate content permissions · Entire portal', exact: true }).check()
  assert.equal(await button(page, 'Save role').isDisabled(), true)
  await page.getByRole('checkbox', { name: 'Read items', exact: true }).check()
  const onward = page.getByRole('checkbox', { name: 'Allow this grant to be delegated', exact: true })
  assert.equal(await onward.isChecked(), false)
  await page.getByRole('checkbox', { name: 'Allow issuing delegable content grants', exact: true }).check()
  assert.equal(await onward.isChecked(), false)
  const collection = page.getByLabel('Collection or installation', { exact: true })
  await collection.selectOption({ label: 'Article' })
  assert.equal(await onward.count(), 0)
  await collection.selectOption({ label: 'Entire portal' })
  assert.equal(await page.getByRole('checkbox', { name: 'Read items', exact: true }).isChecked(), true)
  const accountDomain = page.getByRole('group', { name: 'Accounts', exact: true })
  const accountRead = accountDomain.getByRole('checkbox', { name: 'View accounts · Entire portal', exact: true })
  await accountRead.focus()
  await accountRead.press('Space')
  assert.equal(await accountRead.isChecked(), true)
  await accountRead.press('Space')
  assert.equal(await accountRead.isChecked(), false)
  const response = await pageResponse(page, '/api/v1/admin/roles', 200, () => button(page, 'Save role').click())
  const { grants, state } = await response.json()
  assert.equal(state, 'disabled')
  assert.equal(grants.length, 1)
  assert.equal(grants[0].can_delegate, false)
  assert.equal(grants[0].may_delegate_content, true)
  assert.equal(grants[0].object_ceiling, 'assigned')
}

function grantsForRole(type, stage) {
  const common = type === 'article' ? ['title', 'slug', 'body'] : ['title', 'slug']
  const write = type === 'article' ? common : [...common, 'subtitle']
  const operation = (action, scope, fields) => [`content.items.${action}`, type, scope, fields]
  if (stage === 'author') return [
    operation('create', 'type', []),
    operation('read', 'assigned', write),
    operation('update', 'assigned', write),
    operation('ready', 'assigned', write),
  ]
  return [
    operation('read', 'any', common),
    operation('publish', 'any', write),
    operation('unpublish', 'any', []),
  ]
}

function waitForMembershipSave(page) {
  return page.waitForResponse(response => response.request().method() === 'PUT' &&
    /\/api\/v1\/admin\/users\/[^/]+\/roles$/u.test(new URL(response.url()).pathname) && response.ok())
}

async function saveUserRole(page, login, role) {
  const reading = page.waitForResponse(response => response.request().method() === 'GET' &&
    /\/api\/v1\/admin\/users\/[^/]+\/roles$/u.test(new URL(response.url()).pathname) && response.ok())
  await button(page, 'Manage roles').click()
  const { options } = await (await reading).json()
  const roleID = options.find(option => option.name === role)?.id
  assert.match(roleID, /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/u)
  const group = page.getByRole('group', { name: `Account roles · ${login}`, exact: true })
  const table = group.getByRole('table', { name: 'Content', exact: true })
  const roleBox = table.getByRole('checkbox', { name: role, exact: true })
  assert.equal(await roleBox.isChecked(), false)
  assert.ok(!(await table.innerText()).includes(roleID))
  await roleBox.check()
  assert.ok(await button(page, 'Reload').isDisabled(), 'Other account controls should not discard an in-progress role edit')
  if (login === 'workflow-author') await verifyMembershipConflict(page, group, roleBox)
  const committed = waitForMembershipSave(page)
  await button(group, 'Save role assignments').click()
  assert.deepEqual((await committed).request().postDataJSON().role_ids, [roleID])
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()
  assert.equal(await roleBox.isChecked(), true)
}

async function verifyRoleRemoval(page, theme, fits) {
  const login = 'workflow-author'
  await button(page, 'Access').click()
  await button(page, 'Accounts').click()
  await button(page, login).click()
  await button(page, 'Manage roles').click()
  const group = page.getByRole('group', { name: `Account roles · ${login}`, exact: true })
  const checkbox = group.getByRole('checkbox', { name: 'Workflow author', exact: true })
  assert.ok(await checkbox.isChecked())
  await eachInOrder([[1440, 900], [768, 1024], [390, 844], [320, 844]], async ([width, height]) => {
    await page.setViewportSize({ width, height })
    await eachInOrder(['light', 'dark'], async mode => {
      await chooseTheme(page, mode)
      await theme(page, mode, mode)
      await fits(page)
      assert.ok(await checkbox.isVisible())
    })
  })
  await checkbox.press('Space')
  assert.equal(await checkbox.isChecked(), false)
  const committed = waitForMembershipSave(page)
  await button(group, 'Save role assignments').click()
  assert.deepEqual((await committed).request().postDataJSON().role_ids, [])
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()
  await button(page, 'Roles').click()
  await button(page, 'Accounts').click()
  await button(page, login).click()
  await button(page, 'Manage roles').click()
  assert.equal(await checkbox.isChecked(), false)
}

async function verifyMembershipConflict(page, form, roleBox) {
  await page.route(/\/api\/v1\/admin\/users\/[^/]+\/roles$/u,
    route => route.fulfill({ status: 409, json: { code: 'version_conflict' } }), { times: 1 })
  const save = button(form, 'Save role assignments')
  await save.click()
  await page.getByRole('alert').getByText('This record changed. Reload it and review your changes before retrying.').waitFor()
  assert.ok(await save.isDisabled())
  assert.ok(await roleBox.isChecked())
  await button(form, 'Reload').click()
  await page.waitForFunction(() => document.activeElement instanceof HTMLLegendElement)
  assert.ok(await roleBox.isChecked())
}

async function provision(page, userPage, login, role) {
  await page.getByLabel('Login', { exact: true }).fill(login)
  await button(page, 'Create account').click()
  await heading(page, login).waitFor()
  await button(page, 'Issue invitation').click()
  const issued = page.waitForResponse(r =>
    r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/onboarding-token') && r.ok())
  await button(page, 'Verify').click()
  const { token } = await (await issued).json()
  await page.getByText('Send this one-time token through a verified channel.', { exact: true }).waitFor()
  await button(page, 'Done').click()

  await heading(userPage, 'Sign in').waitFor()
  await button(userPage, 'I have an invitation').click()
  await userPage.getByLabel('Invitation token', { exact: true }).fill(token)
  await userPage.getByLabel('New password', { exact: true }).fill(passphrase)
  const prepared = await pageResponse(userPage, '/api/v1/admin/auth/enrollment', 200, () => button(userPage, 'Continue').click())
  const { secret } = await prepared.json()
  await userPage.getByLabel('Verification code', { exact: true }).fill(totp(secret, -1))
  await button(userPage, 'Verify').click()
  await heading(userPage, 'Recovery codes').waitFor()
  assert.equal(await userPage.locator('.access-codes li').count(), 10)
  await button(userPage, 'Done').click()
  await heading(userPage, 'Sign in').waitFor()

  await button(page, 'Reload').click()
  await button(page, login).click()
  await saveUserRole(page, login, role)
  await button(page, 'Activate account').click()
  await button(page, 'Verify').click()
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()

  await userPage.getByLabel('Login', { exact: true }).fill(login)
  await userPage.getByLabel('Password', { exact: true }).fill(passphrase)
  await button(userPage, 'Continue').click()
  await heading(userPage, 'Two-step verification').waitFor()
  await userPage.getByLabel('Verification code', { exact: true }).fill(totp(secret))
  await button(userPage, 'Verify').click()
  await heading(userPage, 'Signed in').waitFor()
}

async function openCollection(page, type) {
  await button(page, 'Content').click()
  await page.getByRole('combobox', { name: 'Collection', exact: true }).selectOption(type)
}

async function backToDrafts(page) {
  await button(page, 'Back to drafts').click()
  await page.getByRole('region', { name: 'Draft editor', exact: true }).waitFor({ state: 'hidden' })
}

async function writeAsAuthor(page, type, title, slugBase) {
  await openCollection(page, type)
  const creating = waitForContentMutation(page, `/api/v1/admin/content/${type}`, 'POST', 201)
  await button(page, 'Create draft').click()
  const { item_id: id } = await (await creating).json()
  const workspace = page.getByRole('region', { name: 'Draft editor', exact: true })
  await workspace.getByLabel('Language', { exact: true }).selectOption('en')
  await workspace.getByLabel(/^Title(?: \(|$)/).fill(title)
  assert.equal(await workspace.getByLabel(/^URL slug(?: \(|$)/).count(), 0)
  const metadata = await page.evaluate(async path => {
    const response = await fetch(path, { referrerPolicy: 'same-origin' })
    const data = await response.json()
    return { status: response.status, schema_no: data.schema_no, fields: data.fields?.map(field => ({ id: field.field_id, key: field.key, editable: field.editable, scope: field.scope })) }
  }, `/api/v1/admin/content/${type}/${id}/schema`)
  const expectedField = type === 'article' ? 'body' : 'subtitle'
  const extra = metadata.fields?.find(field => field.key === expectedField && field.editable)
  assert.ok(extra, JSON.stringify(metadata))
  const control = workspace.locator(`[id="material-field-${extra.id}"]`)
  assert.ok(await control.isVisible(), JSON.stringify({ field: extra, labels: await workspace.locator('label').allTextContents() }))
  await control.fill('The author saved this text.')
  await assertSavedAddress(page, type, id, button(page, 'Save draft'), 'en', expectedTitleSlug(slugBase, id))
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  assert.equal(await button(page, 'Publish translations').count(), 0)
  await button(page, 'Check translation').click()
  await page.getByText('This translation is ready.', { exact: true }).waitFor()
  await page.getByText('This translation is ready for someone with publishing access.', { exact: true }).waitFor()
  await backToDrafts(page)
  return id
}

async function reopenDraft(page, type, id) {
  await openCollection(page, type)
  await page.locator(`[data-item-id="${id}"]`).getByRole('button').click()
  const workspace = page.getByRole('region', { name: 'Draft editor', exact: true })
  await workspace.getByLabel(/^Title(?: \(|$)/).waitFor()
  return workspace
}

async function renameDraft(page, type, id, title, slug) {
  const workspace = await reopenDraft(page, type, id)
  await workspace.getByLabel('Language', { exact: true }).selectOption('en')
  await workspace.getByLabel(/^Title(?: \(|$)/).fill(title)
  await assertSavedAddress(page, type, id, button(page, 'Save draft'), 'en', slug)
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  await button(page, 'Check translation').click()
  await page.getByText('This translation is ready.', { exact: true }).waitFor()
  await backToDrafts(page)
}

async function manageArticleAssignments(page, id) {
  await reopenDraft(page, 'article', id)
  await button(page, 'Assignees').click()
  const author = page.getByRole('checkbox', { name: 'workflow-author · Active', exact: true })
  const editor = page.getByRole('checkbox', { name: 'workflow-editor · Active', exact: true })
  assert.equal(await author.isChecked(), true)
  assert.equal(await editor.isChecked(), false)
  await editor.check()
  await button(page, 'Review assignments').click()
  await assertEditorMutationBlocksNavigation(page, `**/api/v1/admin/content/article/${id}/assignees`, 'PUT',
    () => button(page, 'Verify').click(), ['Portal settings', 'Back to drafts'])
  await button(page, 'Assignees').click()
  assert.equal(await author.isChecked(), true)
  assert.equal(await editor.isChecked(), true)
  await button(page, 'Draft editor').click()
  await backToDrafts(page)
}

export async function assertEditorMutationBlocksNavigation(page, path, method, send, labels) {
  const held = Promise.withResolvers()
  const pending = Promise.withResolvers()
  const dispatched = Promise.withResolvers()
  let observed = false
  const holdMutation = async route => {
    if (route.request().method() !== method) return route.continue()
    observed = true
    pending.resolve()
    await held.promise
    try { await route.continue() }
    finally { dispatched.resolve() }
  }
  await page.route(path, holdMutation)
  try {
    await send()
    await pending.promise
    await eachInOrder(labels, async label => { assert.ok(await button(page, label).isDisabled(), label) })
  } finally {
    held.resolve()
    if (observed) await dispatched.promise
    await page.unroute(path, holdMutation)
  }
}

function publicLink(type, slug) {
  return `/en/${type === 'article' ? 'articles' : 'events'}/${slug}`
}

async function publishAsEditor(page, visitor, type, id, slug, title) {
  const workspace = await reopenDraft(page, type, id)
  assert.equal(await workspace.getByLabel('subtitle', { exact: true }).count(), 0)
  await button(page, 'Publish translations').click()
  await workspace.getByText('Translations published.', { exact: true }).waitFor()
  const path = publicLink(type, slug)
  const link = workspace.getByRole('link', { name: 'English', exact: true })
  await link.waitFor()
  assert.equal(await link.getAttribute('href'), path)
  const result = await visitor.get(`/api/v1/content/${type}/${slug}?language=en`)
  assert.equal(result.status(), 200)
  const published = await result.json()
  assert.equal(published.localized.title.value, title)
  assert.ok(!Object.hasOwn(published.localized, 'subtitle'))
  const ssr = await visitor.get(path)
  assert.equal(ssr.status(), 200)
  assert.ok((await ssr.text()).includes(title))
  await backToDrafts(page)
}

async function unpublishAsEditor(page, visitor, type, id, paths) {
  await reopenDraft(page, type, id)
  await button(page, 'Unpublish').click()
  await page.getByRole('region', { name: 'Remove this publication and clear readiness?', exact: true }).getByRole('button', { name: 'Confirm', exact: true }).click()
  await page.getByText('Publication removed.', { exact: true }).waitFor()
  await eachInOrder(paths, async path => assert.equal((await visitor.get(path)).status(), 404))
  await backToDrafts(page)
}

async function assertAccessLayout(page, theme, fits) {
  await eachInOrder([[1440, 900], [768, 1024], [390, 844], [320, 844]], async ([width, height]) => {
    await page.setViewportSize({ width, height })
    await eachInOrder(['light', 'dark'], async mode => {
      await chooseTheme(page, mode)
      await theme(page, mode, mode)
      await fits(page)
      assert.ok(await button(page, 'Create account').isVisible())
      console.log(`Access administration visible at ${width}×${height} in ${mode} mode`)
    })
  })
}

async function verifyTargetSessionsRetry(page) {
  const account = page.getByRole('region', { name: 'workflow-editor', exact: true })
  const path = /\/api\/v1\/admin\/users\/[^/]+\/sessions\?/u
  const requireConfirmation = route => route.fulfill({ status: 403, json: { code: 'step_up_required' } })
  const dialog = page.getByRole('dialog', { name: 'Confirm your identity', exact: true })
  const reload = button(account, 'Reload')
  await page.route(path, requireConfirmation, { times: 2 })
  await button(account, 'Active sessions').click()
  await button(dialog, 'Cancel').click()
  assert.equal(await account.getByText('No active sessions.', { exact: true }).count(), 0)
  await reload.click()
  await button(dialog, 'Cancel').click()
  assert.ok(await reload.evaluate(element => element === document.activeElement))
  await reload.click()
  await button(account, 'Revoke session').first().waitFor()
}

export async function verifyAccessAdministration(browser, page, origin, assertTheme, assertFit) {
  const context = await browser.newContext({ baseURL: origin, ignoreHTTPSErrors: true })
  const editors = await browser.newContext({ baseURL: origin, ignoreHTTPSErrors: true })
  const visitor = await browser.newContext({ baseURL: origin, ignoreHTTPSErrors: true })
  try {
    await button(page, 'My account').click()
    await heading(page, 'Active sessions').waitFor()
    await page.getByText('Current session', { exact: true }).waitFor()
    assert.equal(await button(page, 'Close').count(), 0)
    assert.equal(await button(page, 'Sign out').count(), 1)
    await button(page, 'Access').click()
    await button(page, 'Roles').click()
    await proveIndependentDelegationControls(page)
    await stageRole(page, 'Workflow author', ['article', 'event'].flatMap(type => grantsForRole(type, 'author')))
    await stageRole(page, 'Workflow editor', ['article', 'event'].flatMap(type => grantsForRole(type, 'editor')))
    await button(page, 'Accounts').click()
    await assertAccessLayout(page, assertTheme, assertFit)
    const author = await context.newPage()
    const editor = await editors.newPage()
    author.setDefaultTimeout(10000)
    editor.setDefaultTimeout(10000)
    await author.goto('/admin')
    await editor.goto('/admin')
    await provision(page, author, 'workflow-author', 'Workflow author')
    await provision(page, editor, 'workflow-editor', 'Workflow editor')
    await verifyTargetSessionsRetry(page)
    await button(page, 'Audit log').click()
    await page.getByText('Activate account', { exact: true }).first().waitFor()
    assert.equal(await page.getByText('users.activated', { exact: true }).count(), 0)
    assert.equal(await button(page, 'Close').count(), 0)

    await eachInOrder([['article', 'author-feature', 'Author feature'], ['event', 'editor-workshop', 'Editor workshop']], async ([type, slugBase, title]) => {
      const id = await writeAsAuthor(author, type, title, slugBase)
      const slug = expectedTitleSlug(slugBase, id)
      if (type === 'article') await manageArticleAssignments(page, id)
      await publishAsEditor(editor, visitor.request, type, id, slug, title)
      const canonical = expectedTitleSlug(`${slugBase}-revised`, id)
      const renamedTitle = `${title} revised`
      await renameDraft(author, type, id, renamedTitle, canonical)
      await publishAsEditor(editor, visitor.request, type, id, canonical, renamedTitle)
      const paths = [publicLink(type, slug), publicLink(type, canonical)]
      const redirect = await visitor.request.get(paths[0], { maxRedirects: 0 })
      assert.equal(redirect.status(), 301)
      assert.equal(redirect.headers().location, paths[1])
      const apiRedirect = await visitor.request.get(`/api/v1/content/${type}/${slug}?language=en`, { maxRedirects: 0 })
      assert.equal(apiRedirect.status(), 301)
      assert.equal(apiRedirect.headers().location, `/api/v1/content/${type}/${canonical}?language=en`)
      await unpublishAsEditor(editor, visitor.request, type, id, paths)
    })
    await verifyRoleRemoval(page, assertTheme, assertFit)
    await button(author, 'Sign out').click()
    await heading(author, 'Sign in').waitFor()
    await button(editor, 'Sign out').click()
    await heading(editor, 'Sign in').waitFor()
  } finally {
    await Promise.all([context.close(), editors.close(), visitor.close()])
  }
}
