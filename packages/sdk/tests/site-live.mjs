import assert from 'node:assert/strict'
import { createPublicClient } from '@stepolonia/sdk'

const baseUrl = process.env.STAGE22_API_URL
assert.ok(baseUrl, 'STAGE22_API_URL is required')
const client = createPublicClient({ baseUrl })

function assertProblem(result, status, code) {
  assert.equal(result.response.status, status)
  assert.equal(result.data, undefined)
  assert.ok(result.response.headers.get('Content-Type')?.startsWith('application/problem+json'))
  assert.ok(result.error)
  assert.equal(result.error.status, status)
  assert.equal(result.error.code, code)
  assert.deepEqual(Object.keys(result.error).sort(), ['code', 'detail', 'status', 'title', 'type'])
  const body = JSON.stringify(result.error)
  for (const privateDetail of ['SELECT ', 'postgres://', 'stage12_', 'sp_publisher', 'sql:', 'site_revisions']) {
    assert.ok(!body.includes(privateDetail), 'Private dependency details leaked')
  }
}

function assertPublication(result, version, language, requestedLanguage) {
  assert.equal(result.response.status, 200)
  assert.equal(result.error, undefined)
  const site = result.data
  assert.ok(site)
  assert.equal(site.version, version)
  assert.equal(site.language.code, language)
  assert.equal(site.requested_language, requestedLanguage)
  assert.equal(site.settings.default_language, 'pl')
  assert.equal(site.settings.fallback_language, 'en')
  assert.equal(site.settings.title, 'Fixture title')
  assert.deepEqual(Object.keys(site).sort(), ['language', 'languages', 'messages', 'requested_language', 'settings', 'version'])
  assert.deepEqual(Object.keys(site.settings).sort(), ['accent', 'default_language', 'description', 'fallback_language', 'notice', 'tagline', 'title'])
  assert.deepEqual(site.languages.map(item => item.code).sort(), ['en', 'pl'])
  for (const item of [site.language, ...site.languages]) {
    assert.deepEqual(Object.keys(item).sort(), ['code', 'direction', 'name', 'native_name'])
  }
  const keys = ['articles', 'empty', 'pagination', 'previous', 'next', 'navigation', 'all', 'languages', 'errors.400', 'errors.404', 'errors.503', 'errors.other', 'errors.retry', 'errors.home']
  assert.deepEqual(Object.keys(site.messages).sort(), keys.sort())
  for (const key of keys) {
    assert.equal(site.messages[key], `Fixture ${language} ${key}`)
  }
}

if (process.argv.includes('--unavailable')) {
  assertProblem(await client.getSite(), 503, 'site_configuration_unavailable')
  console.log('PASS: SDK -> Go HTTP -> PostgreSQL, missing publication safe 503')
} else {
  const version = process.env.STAGE22_VERSION
  assert.ok(version, 'STAGE22_VERSION from the disposable site fixture is required')
  assertPublication(await client.getSite(), version, 'en', '')
  assertPublication(await client.getSite({ query: { lang: 'PL', version } }), version, 'pl', 'pl')
  assertPublication(await client.getSite({ query: { lang: 'fr' } }), version, 'en', 'fr')
  assertProblem(await client.getSite({ query: { version: '00000000-0000-0000-0000-000000000001' } }), 409, 'site_version_conflict')
  assertProblem(await client.getSite({ query: { version: 'malformed' } }), 400, 'invalid_version')
  console.log('PASS: SDK -> Go HTTP -> PostgreSQL, published catalog/fallback/version/public fields, 200/400/409')
}
