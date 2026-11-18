import assert from 'node:assert/strict'
import { createHmac } from 'node:crypto'

const secretAlphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
const password = 'Browser fixture passphrase 2026'
const button = (page, name) => page.getByRole('button', { name, exact: true })
const heading = (page, name) => page.getByRole('heading', { name, exact: true })

function totp(secret, offset = 0) {
  const bits = [...secret].map(char => secretAlphabet.indexOf(char).toString(2).padStart(5, '0')).join('')
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

export function eachInOrder(values, action) {
  return values.reduce((previous, value) => previous.then(() => action(value)), Promise.resolve())
}

async function stageRole(page, name, grants) {
  await button(page, 'Create role').click()
  await page.getByLabel('Role name', { exact: true }).fill(name)
  await eachInOrder(grants, async ([operation, type, scope, fields]) => {
    await page.getByLabel('Operation and scope', { exact: true }).selectOption({ label: `${operation} · ${type} · ${scope}` })
    await eachInOrder(fields, field => page.getByRole('checkbox', { name: field, exact: true }).check())
    await button(page, 'Add grant').click()
  })
  await button(page, 'Save role').click()
  await heading(page, name).waitFor()
  await button(page, 'Enable').click()
  await button(page, 'Verify').click()
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()
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

async function provision(page, userPage, login, role) {
  await page.getByLabel('Login', { exact: true }).fill(login)
  await button(page, 'Create account').click()
  await heading(page, login).waitFor()
  await button(page, 'Issue invitation').click()
  const issued = page.waitForResponse(r => r.request().method() === 'POST' && /\/onboarding-token$/u.test(new URL(r.url()).pathname) && r.ok())
  await button(page, 'Verify').click()
  const { token } = await (await issued).json()
  await page.getByText('Send this one-time token through a verified channel.', { exact: true }).waitFor()
  await button(page, 'Done').click()

  await heading(userPage, 'Sign in').waitFor()
  await button(userPage, 'I have an invitation').click()
  await userPage.getByLabel('Invitation token', { exact: true }).fill(token)
  await userPage.getByLabel('New password', { exact: true }).fill(password)
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
  await button(page, 'Manage role memberships').click()
  const roleBox = page.getByRole('checkbox', { name: `${role} · Active`, exact: true })
  assert.equal(await roleBox.isChecked(), false)
  await roleBox.check()
  await button(page, 'Review role changes').click()
  await button(page, 'Verify').click()
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()
  assert.equal(await roleBox.isChecked(), true)
  await button(page, 'Activate account').click()
  await button(page, 'Verify').click()
  await page.getByRole('status').getByText('Changes saved.', { exact: true }).waitFor()

  await userPage.getByLabel('Login', { exact: true }).fill(login)
  await userPage.getByLabel('Password', { exact: true }).fill(password)
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

async function closeCollection(page) {
  await button(page, 'Back to drafts').click()
  await button(page, 'Close').click()
}

async function writeAsAuthor(page, type, slug, title) {
  await openCollection(page, type)
  const created = await pageResponse(page, `/api/v1/admin/content/${type}`, 201, () => button(page, 'Create draft').click())
  const { item_id: id } = await created.json()
  const workspace = page.getByRole('region', { name: 'Draft editor', exact: true })
  await workspace.getByLabel('Language', { exact: true }).selectOption('en')
  await workspace.getByLabel(/^title(?: \(|$)/).fill(title)
  await workspace.getByLabel(/^slug(?: \(|$)/).fill(slug)
  const metadata = await page.evaluate(async path => {
    const response = await fetch(path, { referrerPolicy: 'same-origin' })
    const data = await response.json()
    return { status: response.status, schema_no: data.schema_no, fields: data.fields?.map(field => ({ key: field.key, editable: field.editable, scope: field.scope })) }
  }, `/api/v1/admin/content/${type}/${id}/schema`)
  const expectedField = type === 'article' ? 'body' : 'subtitle'
  assert.ok(metadata.fields?.some(field => field.key === expectedField && field.editable), JSON.stringify(metadata))
  const extra = type === 'article' ? /^body(?: \(|$)/ : /^subtitle(?: \(|$)/
  await workspace.getByLabel(extra).fill('The author saved this text.')
  await button(page, 'Save draft').click()
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  assert.equal(await button(page, 'Publish translations').count(), 0)
  await button(page, 'Mark shared fields ready').click()
  await page.getByText('Shared fields are ready.', { exact: true }).waitFor()
  await button(page, 'Mark translation ready').click()
  await page.getByText('This translation is ready.', { exact: true }).waitFor()
  await closeCollection(page)
  return id
}

async function reopenDraft(page, type, id) {
  await openCollection(page, type)
  await button(page, id).click()
  const workspace = page.getByRole('region', { name: 'Draft editor', exact: true })
  await workspace.getByLabel(/^title(?: \(|$)/).waitFor()
  return workspace
}

async function reviseSlug(page, type, id, slug) {
  const workspace = await reopenDraft(page, type, id)
  await workspace.getByLabel('Language', { exact: true }).selectOption('en')
  await workspace.getByLabel(/^slug(?: \(|$)/).fill(slug)
  await button(page, 'Save draft').click()
  await page.getByText('Draft saved.', { exact: true }).waitFor()
  await button(page, 'Mark translation ready').click()
  await page.getByText('This translation is ready.', { exact: true }).waitFor()
  await closeCollection(page)
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
  await button(page, 'Verify').click()
  await button(page, 'Assignees').click()
  assert.equal(await author.isChecked(), true)
  assert.equal(await editor.isChecked(), true)
  await button(page, 'Close').click()
  await closeCollection(page)
}

function publicLink(type, slug) {
  return `/en/${type === 'article' ? 'articles' : 'events'}/${slug}`
}

async function publishAsEditor(page, visitor, type, id, slug, title) {
  const workspace = await reopenDraft(page, type, id)
  assert.equal(await workspace.getByLabel('subtitle', { exact: true }).count(), 0)
  await button(page, 'Publish translations').click()
  await page.getByRole('region', { name: 'Publish the selected translations?', exact: true }).getByRole('button', { name: 'Confirm', exact: true }).click()
  const path = publicLink(type, slug)
  await workspace.getByRole('link', { name: path, exact: true }).waitFor()
  const result = await visitor.get(`/api/v1/content/${type}/${slug}?language=en`)
  assert.equal(result.status(), 200)
  const published = await result.json()
  assert.equal(published.localized.title.value, title)
  assert.ok(!Object.hasOwn(published.localized, 'subtitle'))
  const ssr = await visitor.get(path)
  assert.equal(ssr.status(), 200)
  assert.ok((await ssr.text()).includes(title))
  await closeCollection(page)
}

async function unpublishAsEditor(page, visitor, type, id, paths) {
  await reopenDraft(page, type, id)
  await button(page, 'Unpublish').click()
  await page.getByRole('region', { name: 'Remove this publication and clear readiness?', exact: true }).getByRole('button', { name: 'Confirm', exact: true }).click()
  await page.getByText('Publication removed.', { exact: true }).waitFor()
  await eachInOrder(paths, async path => assert.equal((await visitor.get(path)).status(), 404))
  await closeCollection(page)
}

async function assertAccessLayout(page, theme, fits) {
  await eachInOrder([[1440, 900], [768, 1024], [390, 844], [320, 844]], async ([width, height]) => {
    await page.setViewportSize({ width, height })
    await eachInOrder(['light', 'dark'], async mode => {
      await page.getByLabel('Theme', { exact: true }).selectOption(mode)
      await theme(page, mode, mode)
      await fits(page)
      assert.ok(await button(page, 'Create account').isVisible())
      console.log(`Access administration visible at ${width}×${height} in ${mode} mode`)
    })
  })
}

export async function verifyAccessAdministration(browser, page, origin, assertTheme, assertFit) {
  const context = await browser.newContext({ baseURL: origin, ignoreHTTPSErrors: true })
  const editors = await browser.newContext({ baseURL: origin, ignoreHTTPSErrors: true })
  const visitor = await browser.newContext({ baseURL: origin, ignoreHTTPSErrors: true })
  try {
    await button(page, 'My account').click()
    await heading(page, 'Active sessions').waitFor()
    await page.getByText('Current session', { exact: true }).waitFor()
    await button(page, 'Close').click()
    await button(page, 'Access').click()
    await button(page, 'Roles').click()
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
    await button(page, 'Audit log').click()
    await page.getByText('users.activated', { exact: true }).first().waitFor()
    await button(page, 'Close').click()

    await eachInOrder([['article', 'author-feature', 'Author feature'], ['event', 'editor-workshop', 'Editor workshop']], async ([type, slug, title]) => {
      const id = await writeAsAuthor(author, type, slug, title)
      if (type === 'article') await manageArticleAssignments(page, id)
      await publishAsEditor(editor, visitor.request, type, id, slug, title)
      const canonical = `${slug}-revised`
      await reviseSlug(author, type, id, canonical)
      await publishAsEditor(editor, visitor.request, type, id, canonical, title)
      const paths = [publicLink(type, slug), publicLink(type, canonical)]
      const redirect = await visitor.request.get(paths[0], { maxRedirects: 0 })
      assert.equal(redirect.status(), 301)
      assert.equal(redirect.headers().location, paths[1])
      await unpublishAsEditor(editor, visitor.request, type, id, paths)
    })
    await button(author, 'Sign out').click()
    await heading(author, 'Sign in').waitFor()
    await button(editor, 'Sign out').click()
    await heading(editor, 'Sign in').waitFor()
  } finally {
    await Promise.all([context.close(), editors.close(), visitor.close()])
  }
}
