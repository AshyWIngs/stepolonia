export class AdminContentError extends Error {
  constructor(readonly status: number, readonly code?: string) {
    super('Administration content request failed')
  }
}

export async function administrationRequest<T>(path: string, csrf?: string, method = 'GET', data?: object): Promise<T> {
  return administrationResponse(path, csrf, method, data).then(response => successfulJSON<T>(response))
}

export async function administrationCommand(path: string, csrf: string, data: object, method = 'POST') {
  const response = await administrationResponse(path, csrf, method, data)
  if (response.status !== 204) throw new AdminContentError(503)
}

async function administrationResponse(path: string, csrf: string | undefined, method: string, data: object | undefined): Promise<Response> {
  const options: RequestInit = {
    method,
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    referrerPolicy: 'same-origin',
    signal: AbortSignal.timeout(12000),
  }
  if (data !== undefined) {
    options.headers = { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf ?? '' }
    options.body = JSON.stringify(data)
  }
  const response = await fetch(path, options)
  if (!response.ok) {
    const result: unknown = await response.json().catch(() => null)
    const code = typeof result === 'object' && result !== null && 'code' in result && typeof result.code === 'string'
      ? result.code : undefined
    throw new AdminContentError(response.status, code)
  }
  return response
}

async function successfulJSON<T>(response: Response): Promise<T> {
  const result: unknown = await response.json().catch(() => null)
  if (result === null) throw new AdminContentError(503)
  return result as T
}
