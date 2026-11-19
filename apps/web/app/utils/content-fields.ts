import type { components } from '@stepolonia/sdk'
import { publicError } from './public-content'

export type PublicContent = components['schemas']['PublicItem']
export type PublicField = PublicContent['shared'][string]
export function contentTitle(item: PublicContent): string {
  const title = item.localized.title
  if (title?.kind !== 'string' || typeof title.value !== 'string') throw publicError(503)
  return title.value
}
export function contentBody(item: PublicContent): string {
  const body = item.localized.body
  return body?.kind === 'markdown' ? body.value : ''
}
export function contentFields(item: PublicContent) {
  const fields = [...Object.entries(item.shared), ...Object.entries(item.localized)]
  for (const [, field] of fields) {
    if (!['string', 'text', 'markdown', 'enum', 'integer', 'decimal', 'datetime', 'boolean'].includes(field.kind)
      || (field.kind === 'boolean' ? typeof field.value !== 'boolean' : typeof field.value !== 'string')) throw publicError(503)
  }
  return fields.filter(([key]) => !['title', 'slug'].includes(key)).map(([key, field]) => ({ key, field }))
}

export function readableContentKey(key: string): string {
  return key.replace(/[-_]+/gu, ' ').replace(/^\p{L}/u, letter => letter.toUpperCase())
}

function formattingLocale(language: string): string {
  try { return Intl.getCanonicalLocales(language)[0] ?? 'en' }
  catch { return 'en' }
}

export function formatContentValue(field: Exclude<PublicField, { kind: 'boolean' }>, language: string): string {
  if (field.kind === 'integer' || field.kind === 'decimal') {
    const point = field.value.indexOf('.')
    const fractionDigits = point < 0 ? 0 : field.value.length - point - 1
    return new Intl.NumberFormat(formattingLocale(language), { maximumFractionDigits: fractionDigits })
      .format(field.value as Intl.StringNumericLiteral)
  }
  if (field.kind === 'datetime') {
    const date = new Date(field.value)
    if (Number.isNaN(date.getTime())) throw publicError(503)
    return new Intl.DateTimeFormat(formattingLocale(language), { dateStyle: 'medium', timeStyle: 'long', timeZone: 'UTC' }).format(date)
  }
  return field.kind === 'enum' ? readableContentKey(field.value) : field.value
}

export function relationPath(path: string): string {
  if (!/^\/[a-z]{2,8}(?:-[a-z0-9]{1,8})*\/(?:articles|events)\/[a-z0-9]+(?:-[a-z0-9]+)*$/u.test(path)) throw publicError(503)
  return path
}
