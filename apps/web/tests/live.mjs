import assert from 'node:assert/strict'
import process from 'node:process'
import { chromium } from 'playwright'

const mode = process.argv[2] ?? '--published'
const endpoint = new URL(process.env.STAGE14_API_URL)
assert.equal(endpoint.protocol, 'http:')
assert.equal(endpoint.hostname, '127.0.0.1')
const port = Number(endpoint.port)
assert.ok(Number.isInteger(port) && port > 0 && port <= 65535)
const base = `http://127.0.0.1:${port}`
const currentPath = '/fr/articles/stage15-fr-current'
const oldPath = '/fr/articles/stage15-fr-first'
const articleTitle = 'Bonjour de Stepolonia'

// Старый SDK-сценарий остаётся самостоятельным для Go/HTTP tests без Nuxt.
if (['--published', '--unavailable', '--restored'].includes(mode)) await import('../../../packages/sdk/tests/live.mjs')

async function documentResponse(path, status, headers) {
  const response = await fetch(new URL(path, base), { redirect: 'manual', headers })
  const html = await response.text()
  assert.equal(response.status, status, `SSR ${path}: ${response.status}, expected ${status}`)
  assert.match(response.headers.get('x-robots-tag') ?? '', /noindex/u)
  for (const privateDetail of ['api:8080', 'postgres://', 'db_owner_password', 'stage12_owner', 'stage21-private-token']) {
    assert.ok(!html.includes(privateDetail), `Private SSR detail leaked: ${privateDetail}`)
  }
  return { response, html }
}

function publishedHTML(html) {
  const article = html.match(/<article\b[^>]*>([\s\S]*?)<\/article>/u)?.[1]
  assert.ok(article, 'Published article must be present in server HTML')
  assert.ok(article.includes(articleTitle))
  assert.match(article, /<strong>en français<\/strong>/u)
  assert.ok(!/<(?:script|img|iframe)\b/iu.test(article))
  assert.match(html, /<html\b[^>]*\blang="ru"/u)
  assert.match(html, /<article\b[^>]*\blang="fr"[^>]*\bdir="ltr"/u)
  assert.ok(!/<link\b[^>]*\brel=["']canonical["']/iu.test(html))
  assert.ok(!/<link\b[^>]*\bhreflang=/iu.test(html))
}

async function checkSSR() {
  const listed = await documentResponse('/fr/articles', 200)
  assert.ok(listed.html.includes(articleTitle))
  const { html, response } = await documentResponse(currentPath, 200, {
    Host: 'untrusted.invalid', 'X-Forwarded-Host': 'untrusted.invalid',
    Cookie: 'stage21_private=stage21-private-token', Authorization: 'Bearer stage21-private-token',
  })
  publishedHTML(html)
  assert.match(response.headers.get('cache-control') ?? '', /no-store/u)
  const redirect = await documentResponse(oldPath, 301)
  assert.equal(redirect.response.headers.get('location'), currentPath)
  const target = await documentResponse(redirect.response.headers.get('location'), 200)
  publishedHTML(target.html)
  const missingPages = await Promise.all(['/pl/articles', '/de/articles', '/zz/articles', '/en/articles/stage15-fr-current',
    '/kk/articles/http-private', '/kk/articles/http-first', '/kk/articles/http-final'].map(path => documentResponse(path, 404)))
  for (const missing of missingPages) assert.ok(!missing.html.includes('Title http-private'))
  await documentResponse('/fr/articles/invalid_slug', 400)
  const empty = await documentResponse('/en/articles', 200)
  assert.ok(empty.html.includes('There are no published articles in this language yet.'))
  const links = html.match(/data-language="[^"]+"/gu) ?? []
  assert.deepEqual(links.sort(), ['data-language="fr"', 'data-language="ru"'])
  assert.ok(html.includes('/ru/articles/stage22-ru-current'))
  for (const hiddenSlug of ['stage22-ru-first', 'stage22-ru-draft', 'stage22-kk-private', 'stage22-pl-hidden']) {
    assert.ok(!html.includes(hiddenSlug), `Hidden or obsolete translation link: ${hiddenSlug}`)
  }
}

async function checkBrowser() {
  const browser = await chromium.launch({ headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    await context.addCookies([{ name: 'stage21_private', value: 'browser-private', url: base }])
    const page = await context.newPage()
    const errors = []
    const apiHeaders = []
    page.on('pageerror', error => errors.push(error.message))
    page.on('dialog', dialog => {
      errors.push('Unexpected script dialog')
      dialog.dismiss().catch(error => errors.push(error.message))
    })
    page.on('request', request => {
      if (new URL(request.url()).pathname.startsWith('/api/v1/')) apiHeaders.push(request.allHeaders())
    })
    await page.goto(`${base}/fr/articles`)
    await page.waitForLoadState('networkidle')
    await page.evaluate(() => { window.__stage21Navigation = 'client-document' })
    await page.locator(`a[href="${currentPath}"]`).first().click()
    await page.waitForURL(`${base}${currentPath}`)
    await page.getByRole('heading', { name: articleTitle, level: 1, exact: true }).waitFor()
    assert.equal(await page.evaluate(() => window.__stage21Navigation), 'client-document')
    await safeBrowserArticle(page)
    const redirectResponse = page.waitForResponse(response =>
      new URL(response.url()).pathname === oldPath && response.request().isNavigationRequest())
    // Публичный Vue Router API инициирует клиентский переход на исторический URL,
    // которого уже нет среди ссылок актуального списка.
    await page.evaluate(path => document.querySelector('#__nuxt').__vue_app__.config.globalProperties.$router.push(path), oldPath)
      .catch(error => { if (!error.message.includes('Execution context was destroyed')) throw error })
    const redirected = await redirectResponse
    assert.equal(redirected.status(), 301)
    assert.equal(redirected.headers().location, currentPath)
    await page.waitForURL(`${base}${currentPath}`)
    await page.waitForLoadState('networkidle')
    await page.getByRole('heading', { name: articleTitle, level: 1, exact: true }).waitFor()
    await safeBrowserArticle(page)
    assert.ok(apiHeaders.length > 0)
    for (const headers of await Promise.all(apiHeaders)) {
      assert.equal(headers.cookie, undefined)
      assert.equal(headers.authorization, undefined)
    }
    assert.deepEqual(errors, [])
    await page.setViewportSize({ width: 390, height: 844 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
    await page.screenshot({ path: 'reports/ssr-preview.png', fullPage: true })
  }
  finally {
    await browser.close()
  }
}

async function safeBrowserArticle(page) {
  assert.equal(await page.evaluate(() => window.__stepoloniaXss), undefined)
  assert.equal(await page.locator('article script, article img, article iframe, article [onclick], article [onerror]').count(), 0)
  const links = await page.locator('article a').evaluateAll(elements => elements.map(element => element.getAttribute('href')))
  assert.ok(links.every(href => !/^(?:javascript|data|vbscript|file):/iu.test(href ?? '')))
  assert.equal(await page.locator('html').getAttribute('lang'), 'ru')
  assert.equal(await page.locator('article').getAttribute('lang'), 'fr')
}

async function entryRedirect(origin, headers, language) {
  const response = await fetch(`${origin}/`, { redirect: 'manual', headers })
  assert.ok([302, 307].includes(response.status), `Entry redirect: ${response.status}`)
  assert.equal(new URL(response.headers.get('location'), origin).pathname, `/${language}/articles`)
  assert.match(response.headers.get('cache-control') ?? '', /no-store/u)
}

async function checkLanguageSelection() {
  await Promise.all([
    entryRedirect(base, { 'Accept-Language': 'fr-CA,fr;q=0.9,en;q=0.8' }, 'fr'),
    entryRedirect(base, { 'Accept-Language': 'ru;q=0.1,en;q=0.9' }, 'en'),
    entryRedirect(base, { 'Accept-Language': 'fr;q=0.8,en;q=0.8' }, 'fr'),
    entryRedirect(base, { 'Accept-Language': 'en;q=0,*;q=0.9' }, 'ru'),
    entryRedirect(base, { 'Accept-Language': 'zz' }, 'ru'),
    entryRedirect(base, { 'Accept-Language': 'en;q=2,fr;q=0.8' }, 'fr'),
    entryRedirect(base, { 'Accept-Language': 'en-US;q=1,*;q=0' }, 'en'),
    entryRedirect(base, { 'Accept-Language': 'ru;q=0.1,*;q=0.8' }, 'en'),
    entryRedirect(base, { Cookie: 'sp_content_language=pl', 'Accept-Language': 'en' }, 'en'),
    entryRedirect(base, { Cookie: 'sp_content_language=fr', 'Accept-Language': 'en' }, 'fr'),
  ])
  const direct = await documentResponse(currentPath, 200, {
    'Accept-Language': 'en', Cookie: 'sp_content_language=en',
  })
  publishedHTML(direct.html)
}

async function checkLanguageBrowser() {
  const browser = await chromium.launch({ headless: true })
  try {
    const context = await browser.newContext({ locale: 'en-US' })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => {
      if (/hydration/i.test(message.text())) errors.push(message.text())
    })
    await page.goto(`${base}/fr/articles`)
    await page.waitForLoadState('networkidle')
    await page.evaluate(() => { window.__stage22Navigation = 'same-document' })
    await page.locator(`a[href="${currentPath}"]`).first().click()
    await page.waitForURL(`${base}${currentPath}`)
    await page.getByRole('heading', { name: articleTitle, exact: true }).waitFor()
    await page.locator('[data-testid="content-languages"] a[data-language="ru"]').click()
    await page.waitForURL(`${base}/ru/articles/stage22-ru-current`)
    assert.equal(await page.evaluate(() => window.__stage22Navigation), 'same-document')
    assert.equal(await page.locator('html').getAttribute('lang'), 'ru')
    assert.equal(await page.locator('article').getAttribute('lang'), 'ru')
    await page.goto(`${base}/en/articles`)
    await page.locator('[data-testid="content-languages"] a[data-language="en"]').click()
    await page.reload()
    assert.equal(await page.locator('html').getAttribute('lang'), 'en')
    const saved = (await context.cookies()).find(cookie => cookie.name === 'sp_content_language')
    assert.ok(saved)
    assert.equal(saved.value, 'en')
    assert.equal(saved.domain, '127.0.0.1')
    assert.equal(saved.path, '/')
    assert.equal(saved.sameSite, 'Lax')
    assert.equal(saved.secure, false)
    assert.ok(saved.expires > Date.now() / 1000 + 86400)
    // Новый browser context получает только постоянное cookie-состояние пользователя.
    const resumed = await browser.newContext({ locale: 'ru-RU', storageState: await context.storageState() })
    const resumedPage = await resumed.newPage()
    await resumedPage.goto(`${base}/`)
    await resumedPage.waitForURL(`${base}/en/articles`)
    await resumedPage.goto(`${base}${currentPath}`)
    await resumedPage.getByRole('heading', { name: articleTitle, exact: true }).waitFor()
    assert.equal(await resumedPage.locator('html').getAttribute('lang'), 'ru')
    assert.deepEqual(errors, [])
  }
  finally {
    await browser.close()
  }
}

async function checkOtherInstallation() {
  const second = new URL(process.env.STAGE22_SECOND_URL)
  assert.equal(second.protocol, 'http:')
  assert.equal(second.hostname, '127.0.0.1')
  assert.ok(Number(second.port) > 0 && Number(second.port) <= 65535)
  const origin = second.origin
  await Promise.all([
    entryRedirect(origin, { 'Accept-Language': 'pl-PL,pl;q=0.9,en;q=0.8' }, 'pl'),
    entryRedirect(origin, { 'Accept-Language': 'zz' }, 'pl'),
    entryRedirect(origin, { 'Accept-Language': 'pl;q=0,en;q=0.8' }, 'en'),
    entryRedirect(origin, { 'Accept-Language': 'pl;q=0,*;q=0.9' }, 'en'),
    entryRedirect(origin, { 'Accept-Language': '*' }, 'pl'),
  ])
  const absent = await fetch(`${origin}${currentPath}`)
  assert.equal(absent.status, 404)
  assert.ok(!(await absent.text()).includes(articleTitle))
  const fallback = await fetch(`${origin}/fr/articles`)
  assert.equal(fallback.status, 200)
  assert.match(await fallback.text(), /<html\b[^>]*\blang="en"/u)
  const browser = await chromium.launch({ headless: true })
  try {
    const context = await browser.newContext({ locale: 'ja-JP' })
    await context.addCookies([{ name: 'sp_content_language', value: 'en', url: base }])
    const page = await context.newPage()
    const otherHost = new URL(origin)
    otherHost.hostname = 'localhost'
    await page.goto(otherHost.href)
    await page.waitForURL(`${otherHost.origin}/pl/articles`)
    assert.equal(await page.locator('html').getAttribute('lang'), 'pl')
    assert.ok(!(await page.content()).includes(articleTitle))
    await page.locator('[data-testid="content-languages"] a[data-language="pl"]').click()
    const saved = await context.cookies()
    assert.equal(saved.find(cookie => cookie.domain === 'localhost')?.value, 'pl')
    assert.equal(saved.find(cookie => cookie.domain === '127.0.0.1')?.value, 'en')
    await page.goto(`${base}/`)
    await page.waitForURL(`${base}/en/articles`)
  }
  finally {
    await browser.close()
  }
}

if (mode === '--localization') {
  await checkLanguageSelection()
  await checkLanguageBrowser()
  await checkOtherInstallation()
  console.log('PASS: language negotiation, published translations, persistent choice, SSR/hydration and installation isolation')
}
else if (mode === '--site-default') {
  await entryRedirect(base, { 'Accept-Language': 'zz' }, process.env.STAGE22_DEFAULT)
  console.log('PASS: runtime installation default')
}
else if (mode === '--config-unavailable') {
  await documentResponse('/', 503)
  console.log('PASS: invalid language configuration is a safe 503')
}
else if (mode === '--unavailable' || mode === '--api-unavailable') {
  await documentResponse(currentPath, 503)
  await documentResponse('/fr/articles', 503)
  console.log('PASS: SSR returns safe 503 when the API or database is unavailable')
}
else if (mode === '--restored') {
  publishedHTML((await documentResponse(currentPath, 200)).html)
  console.log('PASS: published SSR content recovers')
}
else {
  await checkSSR()
  await checkBrowser()
  console.log('PASS: SSR HTML/status/redirect, client navigation, Markdown XSS and private-data isolation')
}
