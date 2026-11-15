import { createError } from '#imports'

export function publicError(status: number) {
  const code = [400, 404, 503].includes(status) ? status : 503
  const messages: Record<number, string> = {
    400: 'Bad Request',
    404: 'Not Found',
    503: 'Service Unavailable',
  }
  return createError({ status: code, statusText: messages[code], fatal: true })
}

export function routeSegment(value: string | string[] | undefined): string {
  if (typeof value !== 'string' || !value || value.length > 200 || /[/\\\s\p{Cc}]/u.test(value)) {
    throw publicError(400)
  }
  return value
}

export function articlesPath(language: string, slug?: string): string {
  const path = `/${encodeURIComponent(language)}/articles`
  return slug === undefined ? path : `${path}/${encodeURIComponent(slug)}`
}

export function articleRedirect(location: string | null, language: string, original: string): string {
  // Проверяем контракт API Location и собираем SSR-путь текущей установки.
  const match = location?.match(/^\/api\/v1\/articles\/([a-z0-9]+(?:-[a-z0-9]+)*)\?lang=([^&#]+)$/u)
  if (!match?.[1] || match[1].length > 200 || match[2] !== encodeURIComponent(language)) {
    throw publicError(503)
  }
  const destination = articlesPath(language, match[1])
  if (destination === original) throw publicError(503)
  return destination
}

export function canonicalUrl(origin: string, path: string): string | undefined {
  if (!origin) return undefined
  try {
    const url = new URL(origin)
    const hostname = url.hostname.toLowerCase().replace(/\.$/u, '')
    // Canonical использует явно заданный публичный HTTPS origin установки.
    if (url.protocol !== 'https:' || url.username || url.password || url.pathname !== '/'
      || url.search || url.hash || !hostname.includes('.') || /^[\d.]+$/u.test(hostname)
      || /(?:^|\.)(?:localhost|local|internal|test|invalid|example)$/u.test(hostname)) return undefined
    return new URL(path, url.origin).href
  }
  catch {
    return undefined
  }
}
