import assert from 'node:assert/strict'
import { createPublicClient } from '../src/index.ts'

const baseUrl = process.env.STAGE14_API_URL
assert.ok(baseUrl, 'STAGE14_API_URL is required')
const client = createPublicClient({ baseUrl })
const lang = process.env.STAGE14_ARTICLE_LANG
const slug = process.env.STAGE14_ARTICLE_SLUG
const oldSlug = process.env.STAGE14_REDIRECT_SLUG
assert.ok(lang && slug && oldSlug, 'The disposable publishing fixture is required')

if (process.argv.includes('--unavailable')) {
  const result = await client.getArticle({ path: { slug }, query: { lang } })
  assert.equal(result.response.status, 503)
  assert.equal(result.error.status, 503)
  assert.ok(result.response.headers.get('Content-Type').startsWith('application/problem+json'))
  const body = JSON.stringify(result.error)
  for (const privateDetail of ['SELECT ', 'postgres://', 'stage12_', 'sp_publisher', 'sql:']) {
    assert.ok(!body.includes(privateDetail), 'Private dependency details leaked')
  }
  console.log('PASS: SDK -> Caddy -> unavailable PostgreSQL, safe 503 problem+json')
} else {
  const languages = await client.listLanguages()
  assert.equal(languages.response.status, 200)
  assert.ok(languages.data.some(language => language.code === lang))
  const listed = await client.listArticles({ query: { lang, limit: 1, offset: 0 } })
  assert.equal(listed.response.status, 200)
  assert.equal(listed.data.length, 1)
  assert.equal(listed.data[0].slug, slug)
  const current = await client.getArticle({ path: { slug }, query: { lang } })
  assert.equal(current.response.status, 200)
  assert.equal(current.data.article_id, listed.data[0].article_id)
  assert.equal(current.data.language_code, lang)
  assert.ok(Array.isArray(current.data.published_translations))
  assert.ok(current.data.published_translations.some(translation => translation.language_code === lang && translation.slug === slug))
  await Promise.all(current.data.published_translations.map(async (translation) => {
    const translated = await client.getArticle({ path: { slug: translation.slug }, query: { lang: translation.language_code } })
    assert.equal(translated.response.status, 200)
    assert.equal(translated.data.article_id, current.data.article_id)
    assert.equal(translated.data.slug, translation.slug)
    assert.deepEqual(translated.data.published_translations, current.data.published_translations)
  }))
  const redirect = await client.getArticle({ path: { slug: oldSlug }, query: { lang } })
  assert.equal(redirect.response.status, 301)
  assert.equal(redirect.response.headers.get('Location'), `/api/v1/articles/${slug}?lang=${lang}`)
  assert.equal(redirect.error, null)
  const invalid = await client.listArticles({ query: { lang, limit: 0 } })
  assert.equal(invalid.response.status, 400)
  assert.equal(invalid.error.status, 400)
  const missing = await client.getArticle({ path: { slug: 'sdk-missing' }, query: { lang } })
  assert.equal(missing.response.status, 404)
  assert.equal(missing.error.status, 404)
  console.log('PASS: generated SDK -> HTTP -> runtime PostgreSQL, 200/301/400/404')
}
