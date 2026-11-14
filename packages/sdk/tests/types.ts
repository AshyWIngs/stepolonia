import type { createPublicClient } from '../src/index.ts'
import type { operations } from '../src/schema.d.ts'

// Проверки компилятора защищают обязательные параметры и форму контрактных ответов.
export async function checkClientTypes(client: ReturnType<typeof createPublicClient>) {
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
  return { article, problem }
}

type Redirect = operations['getArticle']['responses'][301]
type Article = operations['getArticle']['responses'][200]['content']['application/json']
type Problem = operations['getArticle']['responses'][503]['content']['application/problem+json']
type Languages = operations['listLanguages']['responses'][200]['content']['application/json']

export function checkResponseTypes(redirect: Redirect, article: Article, problem: Problem, languages: Languages) {
  const location: string = redirect.headers.Location
  const emptyBody: null = redirect.content['application/json']
  const id: string = article.article_id
  const status: number = problem.status
  const catalog: Languages = languages
  // @ts-expect-error Внутренняя ревизия не входит в публичный DTO.
  const internalRevision: string = article.revision_id
  // @ts-expect-error Успешный каталог является массивом.
  const nullCatalog: Languages = null
  return [location, emptyBody, id, status, catalog, internalRevision, nullCatalog]
}
