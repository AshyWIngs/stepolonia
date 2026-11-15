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
if (mode !== '--api-unavailable') await import('../../../packages/sdk/tests/live.mjs')

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
  assert.match(html, /<html\b[^>]*\blang="fr"/u)
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
  for (const path of ['/pl/articles', '/de/articles', '/zz/articles', '/en/articles/stage15-fr-current',
    '/kk/articles/http-private', '/kk/articles/http-first', '/kk/articles/http-final']) {
    const missing = await documentResponse(path, 404)
    assert.ok(!missing.html.includes('Title http-private'))
  }
  await documentResponse('/fr/articles/invalid_slug', 400)
  const empty = await documentResponse('/en/articles', 200)
  assert.ok(empty.html.includes('На этом языке пока нет опубликованных статей.'))
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
  assert.equal(await page.locator('html').getAttribute('lang'), 'fr')
}

if (mode === '--unavailable' || mode === '--api-unavailable') {
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
