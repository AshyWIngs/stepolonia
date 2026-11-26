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
  const result = await client.getContent({ path: { type_key: 'article', slug }, query: { language: lang } })
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
  const listed = await client.listContent({ path: { type_key: 'article' }, query: { language: lang, limit: 1, offset: 0 } })
  assert.equal(listed.response.status, 200)
  assert.equal(listed.data.items.length, 1)
  assert.equal(listed.data.items[0].slug, slug)
  const current = await client.getContent({ path: { type_key: 'article', slug }, query: { language: lang } })
  assert.equal(current.response.status, 200)
  assert.equal(current.data.item_id, listed.data.items[0].item_id)
  assert.equal(current.data.language, lang)
  assert.ok(Array.isArray(current.data.translations))
  assert.ok(current.data.translations.some(translation => translation.language === lang && translation.slug === slug))
  await Promise.all(current.data.translations.map(async (translation) => {
    const translated = await client.getContent({ path: { type_key: 'article', slug: translation.slug }, query: { language: translation.language } })
    assert.equal(translated.response.status, 200)
    assert.equal(translated.data.item_id, current.data.item_id)
    assert.equal(translated.data.slug, translation.slug)
    assert.deepEqual(translated.data.translations, current.data.translations)
  }))
  const redirect = await client.getContent({ path: { type_key: 'article', slug: oldSlug }, query: { language: lang } })
  assert.equal(redirect.response.status, 301)
  assert.equal(redirect.response.headers.get('Location'), `/api/v1/content/article/${slug}?language=${lang}`)
  assert.equal(redirect.error, null)
  const invalid = await client.listContent({ path: { type_key: 'article' }, query: { language: lang, limit: 0 } })
  assert.equal(invalid.response.status, 400)
  assert.equal(invalid.error.status, 400)
  const missing = await client.getContent({ path: { type_key: 'article', slug: 'sdk-missing' }, query: { language: lang } })
  assert.equal(missing.response.status, 404)
  assert.equal(missing.error.status, 404)
  const event = await client.getContent({ path: { type_key: 'event', slug: 'stage32-event' }, query: { language: 'fr' } })
  assert.equal(event.response.status, 200)
  assert.equal(event.data.type_key, 'event')
  assert.equal(event.data.shared.featured.kind, 'boolean')
  assert.equal(typeof event.data.shared.featured.value, 'boolean')
  assert.deepEqual(event.data.field_labels, { featured: 'À la une', price: 'Prix' })
  assert.match(event.data.schema_no, /^[1-9]\d*$/u)
  assert.ok(!JSON.stringify(event.data).includes('private-event-secret'))
  const exact = event.data.shared.price
  assert.equal(exact.kind, 'decimal')
  assert.equal(exact.value, '123456789012.123456')
  const eventPage = await client.listContent({ path: { type_key: 'event' }, query: { language: 'fr' } })
  assert.equal(eventPage.response.status, 200)
  assert.ok(eventPage.data.items.length)
  assert.ok(eventPage.data.items.every(item => !Object.hasOwn(item, 'field_labels')))
  console.log('PASS: generated SDK -> HTTP -> runtime PostgreSQL, 200/301/400/404')
}
