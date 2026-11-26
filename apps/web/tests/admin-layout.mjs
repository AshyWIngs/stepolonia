import assert from 'node:assert/strict'
import { eachInOrder } from './admin-access.mjs'
import { admin, assertFit, collectionButton, heading, origin, readPublicSite, selectInterfaceLanguage, waitForFlag } from './admin-browser-helpers.mjs'
import { assertStablePreferences, chooseTheme, headerGeometry } from './header-preferences.mjs'

export async function verifyNestedNavigation(page) {
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

export async function assertPublishedLocales(page) {
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

export async function verifyWorkspaceGeometry(page) {
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

export async function verifyCompactNavigation(page) {
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
