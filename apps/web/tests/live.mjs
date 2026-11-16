import assert from 'node:assert/strict'
import { get } from 'node:http'
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
const seoOrigin = process.env.STAGE23_ORIGIN ?? ''

// Старый SDK-сценарий остаётся самостоятельным для Go/HTTP tests без Nuxt.
if (['--published', '--unavailable', '--restored'].includes(mode)) await import('../../../packages/sdk/tests/live.mjs')

async function documentResponse(path, status, headers) {
  // Адрес соединения фиксирован loopback-портом harness; Location не меняет назначение запроса.
  // Accept явно выбирает SSR-документ, поскольку Nitro поддерживает также JSON-ошибки.
  const incoming = await new Promise((resolve, reject) => {
    // Внешний бюджет включает внутренний API timeout 5 с и последующий SSR страницы ошибки.
    get({ hostname: '127.0.0.1', port, path, headers: { Accept: 'text/html', ...headers }, signal: AbortSignal.timeout(15000) }, resolve).on('error', reject)
  })
  const chunks = []
  for await (const chunk of incoming) chunks.push(chunk)
  const response = new Response(Buffer.concat(chunks), { status: incoming.statusCode, headers: incoming.headers })
  const html = await response.text()
  assert.equal(response.status, status, `SSR ${path}: ${response.status}, expected ${status}`)
  assert.match(response.headers.get('x-robots-tag') ?? '', /noindex/u)
  for (const privateDetail of ['api:8080', 'postgres://', 'db_owner_password', 'stage12_owner', 'stage21-private-token']) {
    assert.ok(!html.includes(privateDetail), `Private SSR detail leaked: ${privateDetail}`)
  }
  if (status !== 200) assertSeoHTML(html, [], status >= 400)
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
  assertSeoHTML(html, seoOrigin ? articleSeo(seoOrigin) : [])
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
  assert.ok(empty.html.includes('Fixture en empty'))
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
  await browserSeo(page, seoOrigin ? articleSeo(seoOrigin) : [])
  assert.equal(await page.evaluate(() => window.__stepoloniaXss), undefined)
  assert.equal(await page.locator('article script, article img, article iframe, article [onclick], article [onerror]').count(), 0)
  const links = await page.locator('article a').evaluateAll(elements => elements.map(element => element.getAttribute('href')))
  assert.ok(links.every(href => !/^(?:javascript|data|vbscript|file):/iu.test(href ?? '')))
  assert.equal(await page.locator('html').getAttribute('lang'), 'ru')
  assert.equal(await page.locator('article').getAttribute('lang'), 'fr')
}

async function entryRedirect(origin, headers, language, signal = AbortSignal.timeout(5000)) {
  const target = new URL(origin)
  const entryPort = Number(target.port)
  assert.equal(target.protocol, 'http:')
  assert.equal(target.hostname, '127.0.0.1')
  assert.ok(Number.isInteger(entryPort) && entryPort > 0 && entryPort <= 65535)
  const response = await new Promise((resolve, reject) => {
    get({ hostname: '127.0.0.1', port: entryPort, path: '/', headers, signal }, resolve).on('error', reject)
  })
  response.resume()
  assert.ok([302, 307].includes(response.statusCode), `Entry redirect: ${response.statusCode}`)
  assert.equal(new URL(response.headers.location, origin).pathname, `/${language}/articles`)
  assert.match(response.headers['cache-control'] ?? '', /no-store/u)
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
    // Допустимый по размеру header не должен вызывать повторные обходы всего списка предпочтений.
    entryRedirect(base, { 'Accept-Language': `en;q=0,fr;q=0,kk;q=0,ru;q=0${',*'.repeat(7500)}` }, 'ru', AbortSignal.timeout(2000)),
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
    await page.getByRole('heading', { name: 'Title stage22-ru-current', exact: true }).waitFor()
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


function articleSeo(origin) {
  return [
    ['canonical', '', `${origin}${currentPath}`],
    ['alternate', 'fr', `${origin}${currentPath}`],
    ['alternate', 'ru', `${origin}/ru/articles/stage22-ru-current`],
  ]
}

function listSeo(origin, language, languages, page = 1) {
  const path = `${origin}/${language}/articles`
  const canonical = page > 1 ? `${path}?page=${page}` : path
  return [
    ['canonical', '', canonical],
    ...(page === 1 ? languages.map(code => ['alternate', code, `${origin}/${code}/articles`]) : []),
  ]
}

function sortedSeoLinks(links) {
  return links.toSorted((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)))
}

function linkAttribute(tag, name) {
  const marker = ` ${name}="`
  const start = tag.indexOf(marker)
  if (start === -1) return ''
  const valueStart = start + marker.length
  const end = tag.indexOf('"', valueStart)
  return end === -1 ? '' : tag.slice(valueStart, end)
}

function assertSeoHTML(html, expected, robots = true) {
  const links = [...html.matchAll(/<link\b[^>]*>/gu)].map(([tag]) => {
    return ['rel', 'hreflang', 'href'].map(name => linkAttribute(tag, name))
  }).filter(([rel]) => ['canonical', 'alternate'].includes(rel))
  assert.deepEqual(sortedSeoLinks(links), sortedSeoLinks(expected))
  if (robots) assert.match(html, /<meta\b[^>]*name="robots"[^>]*content="noindex, nofollow"/u)
}

async function browserSeo(page, expected) {
  await page.waitForLoadState('networkidle')
  // Router завершает переход до разрешения Nuxt Suspense и обновления head.
  try {
    await page.waitForFunction(expected => {
      const links = [...document.querySelectorAll('head link[rel="canonical"], head link[rel="alternate"]')]
        .map(element => [element.rel, element.hreflang ?? '', element.href])
      const compare = (left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))
      return JSON.stringify(links.toSorted(compare)) === JSON.stringify(expected.toSorted(compare))
    }, expected, { timeout: 10000 })
  }
  catch (error) {
    console.error('SEO navigation state:', await page.evaluate(() => ({
      path: location.pathname + location.search,
      language: document.querySelector('article')?.getAttribute('lang'),
      title: document.querySelector('h1')?.textContent,
      links: [...document.querySelectorAll('head link[rel="canonical"], head link[rel="alternate"]')]
        .map(element => [element.rel, element.hreflang, element.href]),
    })))
    throw error
  }
  const links = await page.locator('head link[rel="canonical"], head link[rel="alternate"]').evaluateAll(elements =>
    elements.map(element => [element.rel, element.hreflang ?? '', element.href]))
  assert.deepEqual(sortedSeoLinks(links), sortedSeoLinks(expected))
}

async function routeTo(page, path) {
  await page.evaluate(path => document.querySelector('#__nuxt').__vue_app__.config.globalProperties.$router.push(path), path)
  await page.waitForURL(`${new URL(page.url()).origin}${path}`)
  await page.waitForLoadState('networkidle')
}

async function checkSeo() {
  const languages = ['en', 'fr', 'kk', 'ru']
  await Promise.all([
    [`${currentPath}?page=2&utm=ignored`, articleSeo(seoOrigin)],
    ['/ru/articles/stage22-ru-current', articleSeo(seoOrigin).map(link => link[0] === 'canonical' ? ['canonical', '', `${seoOrigin}/ru/articles/stage22-ru-current`] : link)],
    ['/fr/articles?page=1&utm=ignored', listSeo(seoOrigin, 'fr', languages)],
    ['/fr/articles?page=2', listSeo(seoOrigin, 'fr', languages, 2)],
    ['/fr/articles?page=3', listSeo(seoOrigin, 'fr', languages, 3)],
    ['/en/articles', listSeo(seoOrigin, 'en', languages)],
  ].map(async ([path, expected]) => assertSeoHTML((await documentResponse(path, 200, {
    Host: 'attacker.invalid', Forwarded: 'host=attacker.invalid;proto=http',
    'X-Forwarded-Host': 'attacker.invalid', 'X-Forwarded-Proto': 'http',
  })).html, expected)))
  await Promise.all(['page=0', 'page=-1', 'page=1.5', 'page=01', 'page=1&page=2', 'page'].map(query => documentResponse(`/fr/articles?${query}`, 400)))
  await checkSSR()
  const other = await fetch(`${process.env.STAGE22_SECOND_URL}/fr/articles`)
  assert.equal(other.status, 200)
  const otherHTML = await other.text()
  assertSeoHTML(otherHTML, listSeo(process.env.STAGE23_SECOND_ORIGIN, 'fr', ['en', 'fr', 'pl']))
  assert.ok(!otherHTML.includes('stage15-fr-current'))
  const browser = await chromium.launch({ headless: true })
  try {
    const page = await browser.newPage()
    const errors = []
    page.on('console', message => { if (/hydration/iu.test(message.text())) errors.push(message.text()) })
    page.on('pageerror', error => errors.push(error.message))
    await page.goto(`${base}${currentPath}`)
    await browserSeo(page, articleSeo(seoOrigin))
    await routeTo(page, '/ru/articles/stage22-ru-current')
    await browserSeo(page, articleSeo(seoOrigin).map(link => link[0] === 'canonical' ? ['canonical', '', `${seoOrigin}/ru/articles/stage22-ru-current`] : link))
    await routeTo(page, '/fr/articles')
    await browserSeo(page, listSeo(seoOrigin, 'fr', languages))
    await routeTo(page, '/fr/articles?page=2')
    await browserSeo(page, listSeo(seoOrigin, 'fr', languages, 2))
    await page.goBack()
    await browserSeo(page, listSeo(seoOrigin, 'fr', languages))
    await page.goForward()
    await browserSeo(page, listSeo(seoOrigin, 'fr', languages, 2))
    await routeTo(page, currentPath)
    await browserSeo(page, articleSeo(seoOrigin))
    await routeTo(page, '/fr/articles/missing-seo')
    await page.getByRole('heading', { level: 1 }).waitFor()
    await browserSeo(page, [])
    await page.goto(`${base}${oldPath}`)
    await browserSeo(page, articleSeo(seoOrigin))
    await routeTo(page, '/fr/articles?page=0')
    await browserSeo(page, [])
    await page.goto(`${base}${currentPath}`)
    await browserSeo(page, articleSeo(seoOrigin))
    // Обрыв браузерного API-запроса проверяет очистку head при клиентской 503.
    // Реальную остановку API/БД отдельно выполняет recovery-сценарий harness.
    await page.route('**/api/v1/articles/**', route => route.abort('connectionrefused'))
    await routeTo(page, '/ru/articles/stage22-ru-current')
    await browserSeo(page, [])
    await page.unroute('**/api/v1/articles/**')
    await page.goto(`${base}${currentPath}`)
    await browserSeo(page, articleSeo(seoOrigin))
    assert.deepEqual(errors, [])
  }
  finally { await browser.close() }
  await checkBrowser()
}

async function checkSeoFixtures() {
  if (mode === '--seo-rejected-origin') {
    assertSeoHTML((await documentResponse('/fr/articles', 200)).html, [])
    assertSeoHTML((await documentResponse('/fr/articles/seo-fixture-21', 200)).html, [])
    return
  }
  if (mode === '--seo-unsupported') {
    assertSeoHTML((await documentResponse('/fr/articles', 200)).html, [['canonical', '', `${seoOrigin}/fr/articles`]])
    await Promise.all(['fr', 'qaa'].map(async (code) => {
      assertSeoHTML((await documentResponse(`/${code}/articles/seo-fixture-21`, 200)).html,
        [['canonical', '', `${seoOrigin}/${code}/articles/seo-fixture-21`]])
    }))
    return
  }
  assertSeoHTML((await documentResponse('/fr/articles', 200)).html,
    listSeo(seoOrigin, 'fr', ['en', 'en-us', 'fr', 'pl', 'zh-hans-cn']))
  await Promise.all(['en-us', 'zh-hans-cn'].map(async (code) => {
    assertSeoHTML((await documentResponse(`/${code}/articles`, 200)).html,
      listSeo(seoOrigin, code, ['en', 'en-us', 'fr', 'pl', 'zh-hans-cn']))
  }))
  const secondPage = await documentResponse('/fr/articles?page=2&utm=x', 200)
  assert.ok(secondPage.html.includes('SEO fixture'))
  assertSeoHTML(secondPage.html, listSeo(seoOrigin, 'fr', [], 2))
  const empty = await documentResponse('/fr/articles?page=3', 200)
  assert.ok(!empty.html.includes('<article'))
  assertSeoHTML(empty.html, listSeo(seoOrigin, 'fr', [], 3))
  const article = await documentResponse('/fr/articles/seo-fixture-21', 200)
  assertSeoHTML(article.html, [['canonical', '', `${seoOrigin}/fr/articles/seo-fixture-21`],
    ['alternate', 'fr', `${seoOrigin}/fr/articles/seo-fixture-21`]])
}

if (mode === '--seo') {
  await checkSeo()
  console.log('PASS: SSR SEO, reciprocal translations, two origins, pagination, navigation and errors')
}
else if (mode.startsWith('--seo-')) {
  await checkSeoFixtures()
  console.log(`PASS: ${mode}`)
}
else if (mode === '--localization') {
  await checkLanguageSelection()
  await checkLanguageBrowser()
  await checkOtherInstallation()
  console.log('PASS: language negotiation, published translations, persistent choice, SSR/hydration and installation isolation')
}
else if (['--site-republished', '--site-draft'].includes(mode)) {
  const { html } = await documentResponse('/en/articles', 200)
  assert.ok(html.includes('Fixture updated'))
  assert.ok(html.includes('Fixture updated en articles'))
  assert.ok(html.includes('Fixture updated en empty @ | &lt;b&gt;literal&lt;/b&gt;'))
  const browser = await chromium.launch({ headless: true })
  try {
    const page = await browser.newPage()
    await page.goto(`${base}/en/articles`)
    await page.getByText('Fixture updated en empty @ | <b>literal</b>', { exact: true }).waitFor()
    assert.equal(await page.locator('section p b').count(), 0)
  }
  finally { await browser.close() }
  assert.ok(!html.includes('Private draft'))
  const dynamic = await documentResponse('/fr/articles', 200)
  assert.match(dynamic.html, /<html\b[^>]*\blang="fr"/u)
  assert.ok(dynamic.html.includes('Fixture updated fr articles'))
  await entryRedirect(base, { 'Accept-Language': 'zz' }, 'en')
  console.log('PASS: published settings and messages update without frontend restart; drafts stay private')
}
else if (mode === '--site-default') {
  await entryRedirect(base, { 'Accept-Language': 'zz' }, process.env.STAGE22_DEFAULT)
  console.log('PASS: runtime installation default')
}
else if (mode === '--config-unavailable') {
  const { html } = await documentResponse('/', 503)
  assert.ok(html.includes('>503<'))
  assert.ok(!html.includes('<header'))
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
