import assert from 'node:assert/strict'
import process from 'node:process'

const endpoint = new URL(process.env.QA_ADMIN_ORIGIN)
assert.equal(endpoint.protocol, 'https:')
assert.equal(endpoint.hostname, '127.0.0.1')
export const { origin } = endpoint
export const admin = new URL('/admin', origin).href

export function heading(page, name) {
  return page.getByRole('heading', { name, exact: true })
}

export async function assertFit(page) {
  const fits = await page.evaluate(() =>
    document.documentElement.scrollWidth <= document.documentElement.clientWidth,
  )
  assert.ok(fits, 'The admin sign-in page must fit without horizontal scrolling')
}

export async function assertTheme(page, preference, actual) {
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

export async function readPublicSite(context) {
  const result = await context.request.get(new URL('/api/v1/site?lang=en', origin).href)
  assert.equal(result.status(), 200)
  return result.json()
}

export async function verifyScopedSaveButton(button, fieldSelector) {
  await button.waitFor()
  assert.equal(await button.locator('svg[aria-hidden="true"]').count(), 1)
  assert.equal((await button.textContent()).trim(), '')
  const box = await button.boundingBox()
  assert.ok(box && box.width >= 44 && box.height >= 44)
  assert.ok(await button.evaluate((element, selector) => Boolean(element.form?.querySelector(selector)), fieldSelector))
}

export async function waitForFlag(control, svg) {
  if (!svg) {
    await control.locator('.language-flag svg').waitFor()
    return
  }
  const source = `data:image/svg+xml,${encodeURIComponent(svg)}`
  await control.locator(`.language-flag img[src=${JSON.stringify(source)}]`).evaluate(image => image.decode())
}

export async function openSection(page, name) {
  const trigger = page.locator('.admin-navigation').getByRole('button', { name, exact: true })
  if (await trigger.getAttribute('aria-expanded') !== 'true') await trigger.click()
}

export function collectionButton(page, domain, label) {
  return page.locator(`#admin-nav-${domain}`).getByRole('button', { name: label, exact: true })
}

export async function selectInterfaceLanguage(page, code) {
  const switcher = page.getByTestId('language-switcher')
  const summary = switcher.locator('summary')
  await summary.press('Enter')
  await switcher.locator(`button[data-language="${code}"]`).click()
  await page.waitForFunction(language => document.documentElement.lang === language, code)
  assert.equal(await summary.getAttribute('data-current-language'), code)
  assert.ok(await summary.evaluate(element => element === document.activeElement))
}
