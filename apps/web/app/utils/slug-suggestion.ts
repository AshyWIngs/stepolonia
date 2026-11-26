export type SlugRule = { language_code: string, character: string, replacement: string }

export const maxSlugRules = 512
const fallbackSlug = 'item'

export function normalizeSlugCharacter(value: string): string {
  return value.normalize('NFC').toLowerCase().normalize('NFC')
}

export function invalidSlugRuleIndex(rules: SlugRule[]): number {
  const seen = new Set<string>()
  return rules.findIndex(({ language_code, character, replacement }, index) => {
    const source = normalizeSlugCharacter(character)
    if (index >= maxSlugRules || !language_code || !/^[^\p{ASCII}\p{White_Space}\p{C}]$/u.test(source) ||
      !/^[a-z0-9-]{0,16}$/u.test(replacement)) return true
    const key = `${language_code}:${source}`
    if (seen.has(key)) return true
    seen.add(key)
    return false
  })
}

export function suggestSlug(title: string, locale: string, rules: SlugRule[], maxLength = 200, minLength = 1): string {
  if (!title.trim()) return ''
  const limit = Math.min(200, maxLength)
  const replacements = new Map(rules.filter(rule => rule.language_code === locale).map(rule => [rule.character, rule.replacement]))
  const mapped = Array.from(title.normalize('NFC'), character => {
    const source = normalizeSlugCharacter(character)
    return replacements.get(source) ?? source
  }).join('')
  const slug = mapped.normalize('NFKD').toLowerCase().replace(/\p{Mark}/gu, '')
    .replace(/[^a-z0-9]+/gu, '-')
    .replace(/^-|-$/gu, '')
    .slice(0, limit)
    .replace(/-$/u, '')
  const base = slug || fallbackSlug.slice(0, limit)
  const missing = Math.min(limit, minLength) - base.length
  if (missing <= 0) return base
  if (missing === 1) return `${base}1`
  const filler = fallbackSlug.repeat(Math.ceil((missing - 1) / fallbackSlug.length)).slice(0, missing - 1)
  return `${base}-${filler}`
}
