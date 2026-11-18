type Preference = { range: string, quality: number }

function validLanguageRange(range: string): boolean {
  if (range === '*') return true
  const subtags = range.split('-')
  const primary = subtags.shift()
  return primary !== undefined && /^[a-z]{1,8}$/iu.test(primary)
    && subtags.every(subtag => /^[a-z0-9]{1,8}$/iu.test(subtag))
}

function parsePreference(value: string): Preference | undefined {
  const sections = value.trim().split(';')
  if (sections.length > 2) return undefined
  const range = sections.at(0)?.trimEnd().toLowerCase()
  if (!range || !validLanguageRange(range)) return undefined
  const parameter = sections.at(1)?.trimStart()
  let quality = 1
  if (parameter !== undefined) {
    if (!/^q=(?:0|1|0\.\d{0,3}|1\.0{0,3})$/iu.exec(parameter)) return undefined
    quality = Number(parameter.slice(2))
  }
  return { range, quality }
}

function rangeMatches(range: string, code: string): boolean {
  return range === '*' || code === range || code.startsWith(`${range}-`)
}

function effectivePreference(code: string, ranges: Map<string, Preference>): Preference | undefined {
  let match = ranges.get('*')
  let candidate = code
  while (candidate) {
    const preference = ranges.get(candidate)
    if (preference && (!match || preference.range.length > match.range.length
      || (preference.range.length === match.range.length && preference.quality > match.quality))) match = preference
    candidate = candidate.replace(/(?:^|-)[^-]+$/u, '')
  }
  return match
}

function eligible(code: string, effective: Map<string, Preference | undefined>, requested: Preference): boolean {
  // A specific range takes precedence over a wildcard or broader range.
  const match = effective.get(code)
  if (!match) return true
  // Keep the quality weight of an explicit range even when the wildcard has a higher weight.
  if (match.range.length > requested.range.length) return false
  return match.quality > 0 || match.range.length < requested.range.length
}

function lookup(preference: Preference, codes: string[], codeSet: Set<string>, effective: Map<string, Preference | undefined>): string | undefined {
  let range = preference.range
  let candidate = range
  while (candidate) {
    if (codeSet.has(candidate) && eligible(candidate, effective, preference)) return candidate
    const match = codes.find(code => rangeMatches(candidate, code) && eligible(code, effective, preference))
    if (match) return match
    // RFC 4647 lookup removes trailing subtags and leaves no dangling extension singleton.
    candidate = candidate.replace(/(?:-[a-z0-9])?-[a-z0-9]+$/u, '')
    if (candidate === range) return undefined
    range = candidate
  }
  return undefined
}

export function preferredContentLanguage(codes: string[], fallback: string, saved: string | null | undefined, header: string): string {
  if (saved && codes.includes(saved)) return saved
  const preferences = header.split(',').map(parsePreference).filter((item): item is Preference => item !== undefined)
  // Index the untrusted header once to avoid quadratic scans.
  const ranges = new Map<string, Preference>()
  for (const preference of preferences) {
    if (preference.quality > (ranges.get(preference.range)?.quality ?? -1)) ranges.set(preference.range, preference)
  }
  const effective = new Map(codes.map(code => [code, effectivePreference(code, ranges)]))
  const codeSet = new Set(codes)
  const wildcardCodes = [fallback, ...codes]
  const ordered = preferences.filter(item => item.quality > 0).sort((left, right) => right.quality - left.quality)
  for (const preference of ordered) {
    if (preference.range === '*') {
      const match = wildcardCodes.find(code => eligible(code, effective, preference))
      if (match) return match
    }
    else {
      const match = lookup(preference, codes, codeSet, effective)
      if (match) return match
    }
  }
  return fallback
}
