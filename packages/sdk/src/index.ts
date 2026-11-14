import createClient from 'openapi-fetch'
import type { operations, paths } from './schema.d.ts'

export type { paths, components, operations } from './schema.d.ts'

export function createPublicClient(options: { baseUrl: string; fetch?: typeof globalThis.fetch }) {
  // Отдельный экземпляр для каждого SSR-запроса; 301 нужен вызывающему коду для canonical redirect.
  const client = createClient<paths>({ ...options, redirect: 'manual' })
  return {
    listLanguages() {
      return client.GET('/api/v1/languages')
    },
    listArticles(params: operations['listArticles']['parameters']) {
      return client.GET('/api/v1/articles', { params })
    },
    async getArticle(params: operations['getArticle']['parameters']) {
      const result = await client.GET('/api/v1/articles/{slug}', { params })
      // openapi-fetch разбирает 301 как error, но его ErrorStatus включает только 4xx/5xx.
      // Явная ветка сохраняет фактический JSON null в публичном типе результата.
      if (result.response.status === 301) {
        return { data: undefined, response: result.response, error: null }
      }
      return result
    },
  }
}
