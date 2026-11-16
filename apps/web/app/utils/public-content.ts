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
  if (!origin || /\s/u.test(origin) || !/^\/(?!\/)[^\\\p{Cc}]*$/u.test(path)) return undefined
  try {
    const url = new URL(origin)
    const hostname = url.hostname.toLowerCase().replace(/\.$/u, '')
    // Canonical использует явно заданный публичный HTTPS origin установки.
    if (url.protocol !== 'https:' || url.username || url.password || url.pathname !== '/'
      || url.search || url.hash || hostname.startsWith('[') || !hostname.includes('.') || /^[\d.]+$/u.test(hostname)
      || /(?:^|\.)(?:localhost|local|internal|test|invalid|example)$/u.test(hostname)) return undefined
    return new URL(path, url.origin).href
  }
  catch {
    return undefined
  }
}

export function articleListPage(value: unknown): number {
  if (value === undefined) return 1
  if (typeof value !== 'string' || !/^[1-9]\d{0,4}$/u.test(value)) throw publicError(400)
  return Number(value)
}

function hreflangParts(code: string) {
  const segments = code.split('-')
  if (segments.some(part => !part)) return undefined
  const [language = '', second, third, ...rest] = segments
  if (rest.length || !/^[a-z]{2}$/u.test(language)) return undefined
  const hasScript = second?.length === 4
  const script = hasScript ? second : undefined
  const region = hasScript ? third : second
  if (!hasScript && third) return undefined
  if (script && !/^[a-z]{4}$/u.test(script)) return undefined
  if (region && !/^[a-z]{2}$/u.test(region)) return undefined
  return { language, script, region }
}

function supportedHreflang(code: string): boolean {
  // Google принимает ISO 639-1 с необязательными ISO 15924 и ISO 3166-1.
  // ICU знает также служебные территории: они не являются странами ISO 3166-1.
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

export function contentSeoLinks(origin: string, path: string, translations: readonly { language_code: string, slug?: string }[]) {
  const canonical = canonicalUrl(origin, path)
  if (!canonical) return []
  const links = [{ rel: 'canonical' as const, href: canonical }]
  const unsupported = translations.filter(item => !supportedHreflang(item.language_code)).map(item => item.language_code)
  if (unsupported.length) {
    console.warn('Набор hreflang пропущен: неподдерживаемые коды языков', unsupported)
    return links
  }
  return [...links, ...translations.map(item => ({
    rel: 'alternate' as const, hreflang: item.language_code,
    href: new URL(articlesPath(item.language_code, item.slug), canonical).href,
  }))]
}
