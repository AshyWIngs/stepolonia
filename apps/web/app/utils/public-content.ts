import { createError } from '#imports'

export function publicError(status: number) {
  const code = [400, 404, 409, 503].includes(status) ? status : 503
  const messages: Record<number, string> = {
    400: 'Bad Request',
    404: 'Not Found',
    409: 'Conflict',
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

export type ContentNamespace = 'articles' | 'events' | `collection-${string}`
export function isContentNamespace(value: string): value is ContentNamespace {
  return value === 'articles' || value === 'events' || (/^collection-[a-z][a-z0-9-]{0,52}$/u.test(value)
    && !['collection-article', 'collection-event', 'collection-renderers'].includes(value))
}
export function contentNamespace(value: string | string[] | undefined): ContentNamespace {
  if (typeof value === 'string' && isContentNamespace(value)) return value
  throw publicError(404)
}
export function contentType(namespace: ContentNamespace): string {
  if (namespace === 'articles') return 'article'
  if (namespace === 'events') return 'event'
  return namespace.slice('collection-'.length).replaceAll('-', '_')
}
export function contentCollectionTitle(namespace: ContentNamespace) {
  return namespace === 'articles' || namespace === 'events' ? namespace : 'collection'
}
export function contentPath(namespace: ContentNamespace, language: string, slug?: string): string {
  const path = `/${encodeURIComponent(language)}/${namespace}`
  return slug === undefined ? path : `${path}/${encodeURIComponent(slug)}`
}
export function contentRedirect(location: string | null, namespace: ContentNamespace, language: string, original: string): string {
  const prefix = `/api/v1/content/${contentType(namespace)}/`
  if (!location?.startsWith(prefix)) throw publicError(503)
  const match = /^([a-z0-9]+(?:-[a-z0-9]+)*)\?language=([^&#]+)$/u.exec(location.slice(prefix.length))
  if (!match?.[1] || match[1].length > 200 || match[2] !== encodeURIComponent(language)) throw publicError(503)
  const destination = contentPath(namespace, language, match[1])
  if (destination === original) throw publicError(503)
  return destination
}

export function canonicalUrl(origin: string, path: string): string | undefined {
  if (!origin || /\s/u.test(origin) || !/^\/(?!\/)[^\\\p{Cc}]*$/u.test(path)) return undefined
  try {
    const url = new URL(origin)
    const hostname = url.hostname.toLowerCase().replace(/\.$/u, '')
    // Canonical URLs use the installation's configured public HTTPS origin.
    if (url.protocol !== 'https:' || url.username || url.password || url.pathname !== '/'
      || url.search || url.hash || hostname.startsWith('[') || !hostname.includes('.') || /^[\d.]+$/u.test(hostname)
      || /(?:^|\.)(?:localhost|local|internal|test|invalid|example)$/u.test(hostname)) return undefined
    return new URL(path, url.origin).href
  }
  catch {
    return undefined
  }
}

export const contentPageSize = 20

export function contentListPage(value: unknown): number {
  if (value === undefined) return 1
  if (typeof value !== 'string' || !/^[1-9]\d{0,4}$/u.test(value)) throw publicError(400)
  const page = Number(value)
  if ((page - 1) * contentPageSize > 10000) throw publicError(400)
  return page
}

function hreflangParts(code: string) {
  const segments = code.split('-')
  if (segments.some(part => !part)) return undefined
  const [language = '', second = '', third, ...rest] = segments
  if (rest.length || !/^[a-z]{2}$/u.test(language)) return undefined
  const hasScript = second.length === 4
  const script = hasScript ? second : undefined
  const region = hasScript ? third : (second || undefined)
  if (!hasScript && third) return undefined
  if (script && !/^[a-z]{4}$/u.test(script)) return undefined
  if (region && !/^[a-z]{2}$/u.test(region)) return undefined
  return { language, script, region }
}

function supportedHreflang(code: string): boolean {
  // Google accepts ISO 639-1 with optional ISO 15924 and ISO 3166-1 subtags.
  // ICU also includes territories outside ISO 3166-1.
  const parts = hreflangParts(code)
  if (!parts) return false
  const { language, script, region } = parts
  const canonical = Intl.getCanonicalLocales(code)[0]?.toLowerCase()
  if (canonical !== code) return false
  const recognized = (type: Intl.DisplayNamesOptions['type'], value: string) =>
    new Intl.DisplayNames(['en'], { type, fallback: 'none' }).of(value) !== undefined
  return recognized('language', language)
    && (!script || recognized('script', script.charAt(0).toUpperCase() + script.slice(1)))
    && (!region || (!['ac', 'cp', 'dg', 'ea', 'eu', 'ez', 'ic', 'qo', 'ta', 'uk', 'un', 'xk', 'zz'].includes(region)
      && recognized('region', region.toUpperCase())))
}

export function contentSeoLinks(origin: string, path: string, namespace: ContentNamespace, translations: readonly { language: string, slug?: string }[]) {
  const canonical = canonicalUrl(origin, path)
  if (!canonical) return []
  const links = [{ rel: 'canonical' as const, href: canonical }]
  const unsupported = translations.filter(item => !supportedHreflang(item.language)).map(item => item.language)
  if (unsupported.length) {
    console.warn('Skipped hreflang set: unsupported language codes', unsupported)
    return links
  }
  return [...links, ...translations.map(item => ({
    rel: 'alternate' as const, hreflang: item.language,
    href: new URL(contentPath(namespace, item.language, item.slug), canonical).href,
  }))]
}
