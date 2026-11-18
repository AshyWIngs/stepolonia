const authPath = '/api/v1/admin/auth'
const sessionKinds = ['preauth', 'login', 'full', 'recovery', 'enrollment'] as const

export type CmsSessionKind = typeof sessionKinds[number]
type CmsAuthAction = 'login' | 'mfa/verify' | 'logout'

export class CmsAuthError extends Error {
  constructor(readonly status: number, readonly code?: string, readonly retryAfter?: number) {
    super('CMS authentication request failed')
  }
}

async function request(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(authPath + path, {
    ...init,
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    referrerPolicy: 'same-origin',
    signal: AbortSignal.timeout(12000),
  })
  if (response.ok) return response
  const problem: unknown = await response.json().catch(() => null)
  const code = problem && typeof problem === 'object' && 'code' in problem && typeof problem.code === 'string'
    ? problem.code : undefined
  const delay = Number(response.headers.get('Retry-After'))
  throw new CmsAuthError(response.status, code, Number.isSafeInteger(delay) && delay > 0 ? delay : undefined)
}

function csrfToken(payload: unknown): string {
  if (payload && typeof payload === 'object' && 'csrf_token' in payload &&
      typeof payload.csrf_token === 'string' && /^[a-zA-Z0-9_-]{43}$/u.test(payload.csrf_token)) {
    return payload.csrf_token
  }
  throw new CmsAuthError(503)
}

function isSessionKind(value: unknown): value is CmsSessionKind {
  return sessionKinds.some(kind => kind === value)
}

export async function restoreCmsSession() {
  const response = await request('/csrf').catch((error: unknown) => {
    if (error instanceof CmsAuthError && error.status === 401) return request('/csrf')
    throw error
  })
  const payload: unknown = await response.json()
  const csrf = csrfToken(payload)
  if (!payload || typeof payload !== 'object' || !('session_kind' in payload) || !isSessionKind(payload.session_kind)) {
    throw new CmsAuthError(503)
  }
  return { csrf, kind: payload.session_kind }
}

export async function submitCmsAuth(action: CmsAuthAction, csrf: string, fields: Record<string, string>) {
  const response = await request('/' + action, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify(fields),
  })
  if (action === 'logout') {
    if (response.status !== 204) throw new CmsAuthError(503)
    return ''
  }
  return csrfToken(await response.json())
}
