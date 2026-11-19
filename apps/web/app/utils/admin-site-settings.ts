import type { components } from '@stepolonia/sdk'
import type { SlugRule } from '~/utils/slug-suggestion'

export type PortalFields = components['schemas']['Settings']
export type PortalLanguage = components['schemas']['Language']

export const portalSettingsPages = {
  identity: { section: 'settings', label: 'settings_identity' },
  appearance: { section: 'settings', label: 'settings_appearance' },
  languages: { section: 'localization', label: 'settings_languages' },
  translations: { section: 'localization', label: 'interface_texts' },
} as const

export type PortalSettingsPage = keyof typeof portalSettingsPages

export interface EditablePortal {
  version: string
  has_draft: boolean
  published_id?: string
  settings: PortalFields
  catalogs: Record<string, Record<string, string>>
  languages: PortalLanguage[]
  message_keys: string[]
  slug_rules: SlugRule[]
  language_flags?: Record<string, string>
}

interface PortalSettingsInput {
  expected_version: string
  slug_rules?: SlugRule[]
  language_flags?: Record<string, string>
  settings: PortalFields
  catalogs: Record<string, Record<string, string>>
}

export class AdminSettingsError extends Error {
  constructor(readonly status: number, readonly code?: string) {
    super('Administration settings request failed')
  }
}

function requestOptions(): RequestInit {
  return {
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    referrerPolicy: 'same-origin',
    signal: AbortSignal.timeout(12000),
  }
}

function mutationOptions(csrf: string, input: PortalSettingsInput): RequestInit {
  return {
    ...requestOptions(),
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify(input),
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function hasVersion(value: unknown): value is Record<string, unknown> & { version: string } {
  return isRecord(value) && typeof value.version === 'string' && /^[1-9]\d{0,18}$/u.test(value.version)
}

function isEditablePortal(value: unknown): value is EditablePortal {
  if (!hasVersion(value) || typeof value.has_draft !== 'boolean') return false
  if (!isPortalFields(value.settings) || !isRecord(value.catalogs)) return false
  if (value.language_flags !== undefined && !isStringRecord(value.language_flags)) return false
  const catalogues = Object.values(value.catalogs)
  return Array.isArray(value.slug_rules) && value.slug_rules.every(isSlugRule) &&
    Array.isArray(value.languages) && value.languages.every(isPortalLanguage) &&
    Array.isArray(value.message_keys) && value.message_keys.every(key => typeof key === 'string') &&
    catalogues.every(isStringRecord)
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every(text => typeof text === 'string')
}

function isSlugRule(value: unknown): value is SlugRule {
  return isRecord(value) && typeof value.language_code === 'string' &&
    typeof value.character === 'string' && typeof value.replacement === 'string'
}

function isPortalFields(value: unknown): value is PortalFields {
  if (!isRecord(value)) return false
  const { title, description, tagline, notice, accent, default_language, fallback_language } = value
  return [title, description, tagline, notice, accent, default_language, fallback_language].every(field => typeof field === 'string')
}

function isPortalLanguage(value: unknown): value is PortalLanguage {
  return isRecord(value) && typeof value.code === 'string' && typeof value.name === 'string' && typeof value.native_name === 'string' &&
    (value.direction === 'ltr' || value.direction === 'rtl')
}

async function readResponse(result: Promise<Response>): Promise<unknown> {
  const response = await result
  const json: unknown = await response.json().catch(() => null)
  if (response.ok) return json
  const code = isRecord(json) && typeof json.code === 'string' ? json.code : undefined
  throw new AdminSettingsError(response.status, code)
}

export async function getPortalSettings(): Promise<EditablePortal> {
  const json = await readResponse(fetch('/api/v1/admin/settings', requestOptions()))
  if (!isEditablePortal(json)) throw new AdminSettingsError(503)
  return json
}

export async function savePortalSettings(csrf: string, input: PortalSettingsInput): Promise<{
  version: string
  publicationID: string
  languageFlags?: Record<string, string>
}> {
  const json = await readResponse(fetch('/api/v1/admin/settings/publication', mutationOptions(csrf, input)))
  if (!hasVersion(json) || typeof json.publication_id !== 'string') throw new AdminSettingsError(503)
  const flags = json.language_flags
  if (flags !== undefined && !isStringRecord(flags)) throw new AdminSettingsError(503)
  return { version: json.version, publicationID: json.publication_id, languageFlags: flags }
}
