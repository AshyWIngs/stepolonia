import type { components } from '@stepolonia/sdk'

export type PortalFields = components['schemas']['Settings']
export type PortalLanguage = components['schemas']['Language']

export interface EditablePortal {
  version: string
  has_draft: boolean
  published_id?: string
  settings: PortalFields
  catalogs: Record<string, Record<string, string>>
  languages: PortalLanguage[]
  message_keys: string[]
}

interface PortalDraftInput {
  expected_version: string
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

function mutationOptions(method: 'PUT' | 'POST', csrf: string, input: PortalDraftInput | { expected_version: string }): RequestInit {
  return {
    ...requestOptions(),
    method,
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
  const catalogues = Object.values(value.catalogs)
  return Array.isArray(value.languages) && value.languages.every(isPortalLanguage) &&
    Array.isArray(value.message_keys) && value.message_keys.every(key => typeof key === 'string') &&
    catalogues.every(entries => isRecord(entries) && Object.values(entries).every(text => typeof text === 'string'))
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

export async function savePortalSettings(csrf: string, input: PortalDraftInput): Promise<string> {
  const json = await readResponse(fetch('/api/v1/admin/settings', mutationOptions('PUT', csrf, input)))
  if (!hasVersion(json)) throw new AdminSettingsError(503)
  return json.version
}

export async function publishPortalSettings(csrf: string, version: string): Promise<string> {
  const options = mutationOptions('POST', csrf, { expected_version: version })
  const json = await readResponse(fetch('/api/v1/admin/settings/publish', options))
  if (!hasVersion(json) || typeof json.publication_id !== 'string') throw new AdminSettingsError(503)
  return json.version
}
