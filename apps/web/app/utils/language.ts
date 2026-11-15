export const uiLocales = ['ru', 'pl', 'kk', 'en'] as const
export type UiLocale = typeof uiLocales[number]

export function isUiLocale(code: string): code is UiLocale {
  return uiLocales.some(locale => locale === code)
}

export function uiFallback(site: { locale: string, uiFallback: string }): UiLocale | undefined {
  const code = site.uiFallback || site.locale
  return isUiLocale(code) ? code : undefined
}

type Preference = { range: string, quality: number }

function parsePreference(value: string): Preference | undefined {
  const match = value.trim().match(/^(\*|[a-z]{1,8}(?:-[a-z0-9]{1,8})*)(?:\s*;\s*q=(0(?:\.\d{0,3})?|1(?:\.0{0,3})?))?$/iu)
  if (!match?.[1]) return undefined
  return { range: match[1].toLowerCase(), quality: Number(match[2] ?? 1) }
}

function rangeMatches(range: string, code: string): boolean {
  return range === '*' || code === range || code.startsWith(`${range}-`)
}

function eligible(code: string, preferences: Preference[], requested: Preference): boolean {
  // Более точный диапазон перекрывает wildcard и общий языковой диапазон.
  const match = preferences.filter(item => rangeMatches(item.range, code))
    .sort((left, right) => right.range.length - left.range.length || right.quality - left.quality)[0]
  if (!match) return true
  // Явный диапазон сохраняет свой q, wildcard не повышает его приоритет.
  if (match.range.length > requested.range.length) return false
  return match.quality > 0 || match.range.length < requested.range.length
}

function lookup(preference: Preference, codes: string[], preferences: Preference[]): string | undefined {
  let range = preference.range
  let candidate = range
  while (candidate) {
    const ordered = [...codes].sort((left, right) => Number(right === candidate) - Number(left === candidate))
    const match = ordered.find(code => rangeMatches(candidate, code) && eligible(code, preferences, preference))
    if (match) return match
    // RFC 4647 lookup отбрасывает регион и одиночный extension subtag вместе с его значением.
    candidate = candidate.replace(/(?:-[a-z0-9])?-[a-z0-9]+$/u, '')
    if (candidate === range) return undefined
    range = candidate
  }
  return undefined
}

export function preferredContentLanguage(codes: string[], fallback: string, saved: string | null | undefined, header: string): string {
  if (saved && codes.includes(saved)) return saved
  const preferences = header.split(',').map(parsePreference).filter((item): item is Preference => item !== undefined)
  const ordered = preferences.filter(item => item.quality > 0).sort((left, right) => right.quality - left.quality)
  for (const preference of ordered) {
    if (preference.range === '*') {
      const match = [fallback, ...codes].find(code => eligible(code, preferences, preference))
      if (match) return match
    }
    else {
      const match = lookup(preference, codes, preferences)
      if (match) return match
    }
  }
  return fallback
}
