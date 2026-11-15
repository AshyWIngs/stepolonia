import type { createPublicClient, operations } from '@stepolonia/sdk'

// Проверки компилятора защищают обязательные параметры и форму контрактных ответов.
export async function checkClientTypes(client: ReturnType<typeof createPublicClient>) {
  void client.getSite()
  void client.getSite({})
  void client.getSite({ query: {} })
  void client.getSite({ query: { lang: 'pl' } })
  void client.getSite({ query: { version: '00000000-0000-0000-0000-000000000001' } })
  const site = await client.getSite({ query: { lang: 'pl', version: '00000000-0000-0000-0000-000000000001' } })
  const publication: Publication | undefined = site.data
  const siteProblem: SiteConflict | SiteUnavailable | undefined = site.error
  // @ts-expect-error Язык передаётся строкой.
  void client.getSite({ query: { lang: 1 } })
  // @ts-expect-error Версия публикации передаётся строкой.
  void client.getSite({ query: { version: 1 } })
  // @ts-expect-error Публичная операция не принимает Cookie.
  void client.getSite({ cookie: { session: 'private' } })
  void client.listLanguages()
  void client.listArticles({ query: { lang: 'pl' } })
  void client.listArticles({ query: { lang: 'pl', limit: 20, offset: 0 } })
  const detail = await client.getArticle({ path: { slug: 'article' }, query: { lang: 'pl' } })
  const article: Article | undefined = detail.data
  const problem: Problem | null | undefined = detail.error
  // @ts-expect-error Язык обязателен.
  void client.listArticles({ query: { limit: 20 } })
  // @ts-expect-error Пагинация требует числового значения.
  void client.listArticles({ query: { lang: 'pl', limit: '20' } })
  // @ts-expect-error Адрес статьи обязателен.
  void client.getArticle({ query: { lang: 'pl' } })
  return { article, problem, publication, siteProblem }
}

type Redirect = operations['getArticle']['responses'][301]
type Article = operations['getArticle']['responses'][200]['content']['application/json']
type Problem = operations['getArticle']['responses'][503]['content']['application/problem+json']
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
  // @ts-expect-error Редакционная версия закрыта публичному клиенту.
  const draftVersion: number = site.draft_version
  return [version, defaultLanguage, fallbackLanguage, requestedLanguage, language, direction, languages, messages, conflictCode, unavailableStatus, draftVersion]
}

export function checkResponseTypes(redirect: Redirect, article: Article, problem: Problem, languages: Languages) {
  const location: string = redirect.headers.Location
  const emptyBody: null = redirect.content['application/json']
  const id: string = article.article_id
  const translations: { language_code: string; slug: string }[] = article.published_translations
  const status: number = problem.status
  const catalog: Languages = languages
  // @ts-expect-error Внутренняя ревизия не входит в публичный DTO.
  const internalRevision: string = article.revision_id
  // @ts-expect-error Успешный каталог является массивом.
  const nullCatalog: Languages = null
  return [location, emptyBody, id, translations, status, catalog, internalRevision, nullCatalog]
}
