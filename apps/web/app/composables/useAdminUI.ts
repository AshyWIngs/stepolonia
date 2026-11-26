import type { components } from '@stepolonia/sdk'
import { useRuntimeConfig } from '#imports'
import { administrationRequest } from '~/utils/admin-request'
import { publicError } from '~/utils/public-content'
import type { SlugRule } from '~/utils/slug-suggestion'

type UILanguage = components['schemas']['Language']

export type AdminEntryCatalog = {
  version: string
  title: string
  language: UILanguage
  languages: UILanguage[]
  messages: Record<string, string>
  language_flags?: Record<string, string>
}

export type AdminWorkspaceCatalog = AdminEntryCatalog & {
  account_id: string
  default_language: string
  slug_rules: SlugRule[]
}

const entryPath = '/api/v1/admin/ui/entry'
const workspacePath = '/api/v1/admin/ui/catalog'

function query(language?: string | null) {
  return language ? '?' + new URLSearchParams({ lang: language }) : ''
}

export function useAdminUI() {
  const config = useRuntimeConfig()
  const origin = import.meta.server ? config.apiOrigin : window.location.origin

  async function readEntry(language?: string | null): Promise<AdminEntryCatalog> {
    const read = (lang?: string | null) => fetch(new URL(entryPath + query(lang), origin), {
      credentials: 'omit', cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(5000),
    })
    let response = await read(language)
    if (response.status === 400 && language) response = await read()
    if (!response.ok) throw publicError(503)
    const catalog = await response.json() as AdminEntryCatalog
    if (!catalog?.messages?.['admin.sign_in']) throw publicError(503)
    return catalog
  }

  function readWorkspace(language: string) {
    return administrationRequest<AdminWorkspaceCatalog>(workspacePath + query(language))
  }

  return { readEntry, readWorkspace }
}
