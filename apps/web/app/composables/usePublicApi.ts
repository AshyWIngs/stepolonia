import { createPublicClient } from '@stepolonia/sdk'
import { useRequestHeader, useRuntimeConfig } from '#imports'
import { publicError } from '~/utils/public-content'

export function usePublicApi() {
  const config = useRuntimeConfig()
  const requestId = useRequestHeader('x-request-id')?.trim() || (import.meta.server ? crypto.randomUUID() : undefined)
  const baseUrl = import.meta.server ? config.apiOrigin : window.location.origin
  // Scope SDK options to the current request.
  const client = createPublicClient({
    baseUrl,
    fetch: (input, init) => globalThis.fetch(input, {
      ...init, credentials: 'omit', cache: 'no-store',
      // Allow five seconds for the three-second API timeout, transport, and serialization.
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
      // Use a safe public error so fetch exceptions cannot expose internal addresses.
      throw publicError(503)
    }
  }

  function diagnostic(code: string, cause?: string) {
    if (import.meta.server) console.warn(JSON.stringify({ event: 'public_content_unavailable', status: 503, code, request_id: requestId, cause }))
  }

  return { client, request }
}

function failureCode(error: unknown): string {
  if (!(error instanceof Error)) return 'unknown'
  const cause = error.cause
  if (cause && typeof cause === 'object' && 'code' in cause && typeof cause.code === 'string') {
    return cause.code
  }
  return error.name
}
