import type { components } from '@stepolonia/sdk'
import type { SlugRule } from '~/utils/slug-suggestion'
import { retryAfterSeconds } from '~/utils/admin-request'

export type PortalFields = components['schemas']['Settings']
export type PortalDetails = Pick<PortalFields, 'title' | 'tagline' | 'description' | 'notice'>
export type PortalLocaleDefaults = Pick<PortalFields, 'default_language' | 'fallback_language'>
export type SlugRuleKey = Pick<SlugRule, 'language_code' | 'character'>
export type PortalLanguage = components['schemas']['Language']

export const portalSettingsPages = {
  identity: { section: 'settings', label: 'admin.settings_identity' },
  appearance: { section: 'settings', label: 'admin.settings_appearance' },
  languages: { section: 'localization', label: 'admin.settings_languages' },
  translations: { section: 'localization', label: 'common.interface_texts' },
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
  appearance_version: string
  details_version: string
  locale_defaults_version: string
  slug_rules_version: string
  catalog_versions: Record<string, string>
  language_flag_versions: Record<string, string>
}

interface PortalSettingsInput {
  expected_version: string
  slug_rules?: SlugRule[]
  language_flags?: Record<string, string>
  settings: PortalFields
  catalogs: Record<string, Record<string, string>>
}

export class AdminSettingsError extends Error {
  constructor(readonly status: number, readonly code?: string, readonly retryAfter?: number) {
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
  if (!validScopeVersions(value)) return false
  const catalogues = Object.values(value.catalogs)
  return Array.isArray(value.slug_rules) && value.slug_rules.every(isSlugRule) &&
    Array.isArray(value.languages) && value.languages.every(isPortalLanguage) &&
    Array.isArray(value.message_keys) && value.message_keys.every(key => typeof key === 'string') &&
    catalogues.every(isStringRecord)
}

function validScopeVersions(value: Record<string, unknown>) {
  return typeof value.appearance_version === 'string' && typeof value.details_version === 'string' &&
    typeof value.locale_defaults_version === 'string' && typeof value.slug_rules_version === 'string' &&
    isStringRecord(value.language_flag_versions) && isStringRecord(value.catalog_versions)
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
  throw new AdminSettingsError(response.status, code, retryAfterSeconds(response.headers))
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

export type ScopedSettingsSection = 'details' | 'appearance' | 'language-flags' | 'locale-defaults' | 'slug-rules' | 'catalogs'

export type ScopedSettingsInput = { expected_version: string } & (
  { details: PortalDetails } | { accent: string } | { language_code: string, svg: string } |
  { defaults: PortalLocaleDefaults } | { upsert?: SlugRule[], remove?: SlugRuleKey[] } |
  { language_code: string, messages?: Record<string, string>, remove_catalog?: true }
)

function postScopedSettings(scope: ScopedSettingsSection, options: RequestInit) {
  switch (scope) {
    case 'details': return fetch('/api/v1/admin/settings/details', options)
    case 'appearance': return fetch('/api/v1/admin/settings/appearance', options)
    case 'language-flags': return fetch('/api/v1/admin/settings/language-flags', options)
    case 'locale-defaults': return fetch('/api/v1/admin/settings/locale-defaults', options)
    case 'slug-rules': return fetch('/api/v1/admin/settings/slug-rules', options)
    case 'catalogs': return fetch('/api/v1/admin/settings/catalogs', options)
  }
  throw new AdminSettingsError(400)
}

export async function saveScopedPortalSetting(
  scope: ScopedSettingsSection,
  csrf: string,
  input: ScopedSettingsInput,
) {
  const json = await readResponse(postScopedSettings(scope, {
    ...requestOptions(),
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify(input),
  }))
  if (!hasVersion(json) || typeof json.previous_version !== 'string' || typeof json.publication_id !== 'string' ||
      typeof json.scope_version !== 'string' || !validScopedValue(scope, json.value)) throw new AdminSettingsError(503)
  return {
    version: json.version, previousVersion: json.previous_version, publicationID: json.publication_id,
    scopeVersion: json.scope_version, value: typeof json.value === 'string' ? json.value : '',
  }
}

function validScopedValue(scope: ScopedSettingsSection, value: unknown) {
  return scope !== 'appearance' && scope !== 'language-flags' || typeof value === 'string'
}

const languageDirectoryURL = '/api/v1/admin/settings/languages'

export interface LanguageDirectoryEntry {
  language: PortalLanguage
  enabled: boolean
  catalog_published: boolean
  version: string
}

type LanguageDirectoryCommand =
  | { language: PortalLanguage, expected_version?: string }
  | { code: string, enabled: boolean, expected_version: string }

function isLanguageDirectoryEntry(value: unknown): value is LanguageDirectoryEntry {
  return isRecord(value) && typeof value.version === 'string' && /^[0-9a-f]{64}$/u.test(value.version) &&
    isPortalLanguage(value.language) &&
    typeof value.enabled === 'boolean' && typeof value.catalog_published === 'boolean'
}

export async function getLanguageDirectory(): Promise<LanguageDirectoryEntry[]> {
  const json = await readResponse(fetch(languageDirectoryURL, requestOptions()))
  if (!isRecord(json) || !Array.isArray(json.items) || !json.items.every(isLanguageDirectoryEntry)) {
    throw new AdminSettingsError(503)
  }
  return json.items
}

export async function saveLanguageDirectoryEntry(csrf: string, command: LanguageDirectoryCommand) {
  const url = 'enabled' in command ? `${languageDirectoryURL}/availability` : languageDirectoryURL
  const json = await readResponse(fetch(url, {
    ...requestOptions(),
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify(command),
  }))
  if (!isLanguageDirectoryEntry(json)) throw new AdminSettingsError(503)
  return json
}
