import type { createPublicClient, operations } from '@stepolonia/sdk'

// Compiler checks cover required parameters and response shapes.
export async function checkClientTypes(client: ReturnType<typeof createPublicClient>) {
  void client.getSite()
  void client.getSite({})
  void client.getSite({ query: {} })
  void client.getSite({ query: { lang: 'pl' } })
  void client.getSite({ query: { version: '00000000-0000-0000-0000-000000000001' } })
  const site = await client.getSite({ query: { lang: 'pl', version: '00000000-0000-0000-0000-000000000001' } })
  const publication: Publication | undefined = site.data
  const siteProblem: SiteConflict | SiteUnavailable | undefined = site.error
  // @ts-expect-error Language must be a string.
  void client.getSite({ query: { lang: 1 } })
  // @ts-expect-error Publication version must be a string.
  void client.getSite({ query: { version: 1 } })
  // @ts-expect-error The public operation accepts no Cookie header.
  void client.getSite({ cookie: { session: 'private' } })
  void client.listLanguages()
  void client.listContent({ path: { type_key: 'article' }, query: { language: 'pl' } })
  void client.listContent({ path: { type_key: 'article' }, query: { language: 'pl', limit: 20, offset: 0 } })
  const detail = await client.getContent({ path: { type_key: 'article', slug: 'article' }, query: { language: 'pl' } })
  const article: Content | undefined = detail.data
  const problem: Problem | null | undefined = detail.error
  // @ts-expect-error Language is required.
  void client.listContent({ path: { type_key: 'article' }, query: { limit: 20 } })
  // @ts-expect-error Pagination requires a numeric value.
  void client.listContent({ path: { type_key: 'article' }, query: { language: 'pl', limit: '20' } })
  // @ts-expect-error The article slug is required.
  void client.getContent({ query: { language: 'pl' } })
  return { article, problem, publication, siteProblem }
}

type Redirect = operations['getContent']['responses'][301]
type Content = operations['getContent']['responses'][200]['content']['application/json']
type Problem = operations['getContent']['responses'][503]['content']['application/problem+json']
type Languages = operations['listLanguages']['responses'][200]['content']['application/json']
type Publication = operations['getSite']['responses'][200]['content']['application/json']
type SiteConflict = operations['getSite']['responses'][409]['content']['application/problem+json']
type SiteUnavailable = operations['getSite']['responses'][503]['content']['application/problem+json']

export function checkSiteResponseTypes(site: Publication, conflict: SiteConflict, unavailable: SiteUnavailable) {
  const version: string = site.version
  const defaultLanguage: string = site.settings.default_language
  const fallbackLanguage: string = site.settings.fallback_language
  const requestedLanguage: string = site.requested_language
  const language: string = site.language.code
  const direction: 'ltr' | 'rtl' = site.language.direction
  const languages: Publication['languages'] = site.languages
  const messages: Record<string, string> = site.messages
  const conflictCode: string = conflict.code
  const unavailableStatus: number = unavailable.status
  // @ts-expect-error The editorial version is private.
  const draftVersion: number = site.draft_version
  return [version, defaultLanguage, fallbackLanguage, requestedLanguage, language, direction, languages, messages, conflictCode, unavailableStatus, draftVersion]
}

export function checkResponseTypes(redirect: Redirect, article: Content, problem: Problem, languages: Languages) {
  const location: string = redirect.headers.Location
  const emptyBody: null = redirect.content['application/json']
  const id: string = article.item_id
  const translations: { language: string; slug: string }[] = article.translations
  const status: number = problem.status
  const catalog: Languages = languages
  // @ts-expect-error The internal revision is private.
  const internalRevision: string = article.revision_id
  // @ts-expect-error The successful catalog is an array.
  const nullCatalog: Languages = null
  return [location, emptyBody, id, translations, status, catalog, internalRevision, nullCatalog]
}

export function checkTaggedValues(item: Content) {
  const version: string = item.schema_no
  const field = item.shared.price
  if (field?.kind === 'decimal' || field?.kind === 'integer') {
    const exact: string = field.value
    // @ts-expect-error Exact scalar codecs cannot become floating point numbers.
    const lossy: number = field.value
    return [version, exact, lossy]
  }
  const boolean = item.shared.featured
  if (boolean?.kind === 'boolean') {
    const value: boolean = boolean.value
    // @ts-expect-error The discriminator preserves JSON boolean values.
    const stringValue: string = boolean.value
    return [value, stringValue]
  }
  return []
}
