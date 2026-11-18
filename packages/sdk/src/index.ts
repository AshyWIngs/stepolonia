import createClient from 'openapi-fetch'
import type { operations, paths } from './schema.d.ts'

export type { paths, components, operations } from './schema.d.ts'

export function createPublicClient(options: { baseUrl: string; fetch?: typeof globalThis.fetch }) {
  // Use one client per SSR request. Callers read 301 to issue a canonical redirect.
  const client = createClient<paths>({ ...options, redirect: 'manual' })
  return {
    getSite(params?: operations['getSite']['parameters']) {
      return client.GET('/api/v1/site', { params: params ?? {} })
    },
    listLanguages() {
      return client.GET('/api/v1/languages')
    },
    listContent(params: operations['listContent']['parameters']) {
      return client.GET('/api/v1/content/{type_key}', { params })
    },
    async getContent(params: operations['getContent']['parameters']) {
      const result = await client.GET('/api/v1/content/{type_key}/{slug}', { params })
      // openapi-fetch sends 301 through its error path, whose ErrorStatus covers only 4xx and 5xx.
      // Handle the redirect explicitly to retain the JSON null result type.
      if (result.response.status === 301) {
        return { data: undefined, response: result.response, error: null }
      }
      return result
    },
  }
}
