import { createPublicClient, type components } from '@stepolonia/sdk'
import { useRequestHeader, useRuntimeConfig } from '#imports'
import { articleRedirect, articlesPath, publicError } from '~/utils/public-content'

type Language = components['schemas']['LanguageDTO']

export function usePublicContent() {
  const config = useRuntimeConfig()
  const requestId = useRequestHeader('x-request-id')?.trim() || (import.meta.server ? crypto.randomUUID() : undefined)
  const baseUrl = import.meta.server ? config.apiOrigin : window.location.origin
  // SDK-контекст привязан к текущему запросу и явно заданным параметрам fetch.
  const client = createPublicClient({
    baseUrl,
    fetch: (input, init) => globalThis.fetch(input, {
      ...init, credentials: 'omit', cache: 'no-store',
      // Пять секунд учитывают трёхсекундный API timeout, сеть и сериализацию.
      signal: AbortSignal.any([...(input instanceof Request ? [input.signal] : []), AbortSignal.timeout(5000)]),
    }),
  })

  async function request<T extends { response: Response }>(operation: () => Promise<T>): Promise<T> {
    try {
      const result = await operation()
      if (result.response.status >= 500) diagnostic('api_unavailable', `http_${result.response.status}`)
      return result
    }
    catch (error) {
      diagnostic('api_connection_failed', failureCode(error))
      // Безопасная публичная ошибка защищает внутренние адреса из исключений fetch.
      throw publicError(503)
    }
  }

  function diagnostic(code: string, cause?: string) {
    if (import.meta.server) console.warn(JSON.stringify({ event: 'public_content_unavailable', status: 503, code, request_id: requestId, cause }))
  }

  async function language(code: string): Promise<Language> {
    const { data, response } = await request(() => client.listLanguages())
    if (!data) throw publicError(response.status)
    const selected = data.find(item => item.code === code)
    if (!selected) throw publicError(404)
    return selected
  }

  async function list(code: string, offset: number) {
    const selected = await language(code)
    const { data, response } = await request(() => client.listArticles({ query: { lang: selected.code, limit: 20, offset } }))
    if (!data) throw publicError(response.status)
    return { articles: data, language: selected }
  }

  async function article(code: string, slug: string) {
    const selected = await language(code)
    const result = await request(() => client.getArticle({ path: { slug }, query: { lang: selected.code } }))
    const source = articlesPath(selected.code, slug)
    // Браузер скрывает Location при manual redirect. Полный переход возвращает обработку 301 серверу.
    if (import.meta.client && result.response.type === 'opaqueredirect') {
      return { kind: 'redirect' as const, path: source }
    }
    if (result.response.status === 301) {
      return { kind: 'redirect' as const, path: articleRedirect(result.response.headers.get('location'), selected.code, source) }
    }
    if (!result.data) throw publicError(result.response.status)
    return { kind: 'article' as const, article: result.data, language: selected }
  }

  return { list, article }
}

function failureCode(error: unknown): string {
  if (!(error instanceof Error)) return 'unknown'
  const cause = error.cause
  if (cause && typeof cause === 'object' && 'code' in cause && typeof cause.code === 'string') {
    return cause.code
  }
  return error.name
}
