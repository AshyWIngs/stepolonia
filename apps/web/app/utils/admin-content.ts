export class AdminContentError extends Error {
  constructor(readonly status: number, readonly code?: string) {
    super('Administration content request failed')
  }
}

export interface CollectionChoice {
  type_id: string
  type_key: string
  schema_no: string
  can_create: boolean
}

interface ContentPage<T> {
  items: T[]
  next_offset: number | null
}

export interface DraftSummary {
  item_id: string
  type_id: string
  schema_no: string
  version: string
}

export interface DraftRelation {
  field_id: string
  locale: string
  target_type_id: string
  target_item_id: string
}

export interface EditableDraft extends DraftSummary {
  shared: Record<string, Scalar>
  shared_ready_seq: string | null
  locales: Record<string, { values: Record<string, Scalar>, ready: object | null }>
  relations: DraftRelation[]
  publication_id?: string
}

export type Scalar = string | boolean
export type FieldKind = 'string' | 'text' | 'markdown' | 'enum' | 'integer' | 'decimal' | 'datetime' | 'boolean' | 'relation'

export interface EditorField {
  field_id: string
  key: string
  kind: FieldKind
  scope: 'shared' | 'localized'
  visibility: 'public' | 'private'
  required: boolean
  editable: boolean
  config: { min_length?: number, max_length?: number, min?: string, max?: string, codes?: string[], slug?: boolean }
  display_labels: Record<string, { label?: string, help?: string }>
}

export interface EditorMetadata {
  item_id: string
  schema_no: string
  current_schema_no: string
  version: string
  fields: EditorField[]
  actions: { shared_ready: boolean, ready: boolean, publish: boolean, unpublish: boolean, adopt: boolean }
  published: { language: string, canonical_url: string }[]
  ready_locales: string[]
}

interface ScalarPatch {
  values?: Record<string, { kind: FieldKind, value: Scalar }>
  unset?: string[]
}

export interface ContentPatch {
  shared?: ScalarPatch['values']
  unset_shared?: string[]
  locales?: Record<string, ScalarPatch>
  relation_changes?: { field_id: string, locale?: string, target_item_id?: string }[]
}

export type DraftCommand = 'ready-shared' | 'ready' | 'publish' | 'unpublish' | 'adopt-schema'

const collectionsPath = '/api/v1/admin/content-types'
const contentPrefix = '/api/v1/admin/content'

function collectionPath(type: string) {
  return `${contentPrefix}/${encodeURIComponent(type)}`
}

function draftPath(type: string, itemID: string) {
  return `${collectionPath(type)}/${encodeURIComponent(itemID)}`
}

async function administrationRequest<T>(path: string, csrf?: string, method = 'GET', data?: object): Promise<T> {
  const options: RequestInit = {
    method,
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    referrerPolicy: 'same-origin',
    signal: AbortSignal.timeout(12000),
  }
  if (data !== undefined) {
    options.headers = { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf ?? '' }
    options.body = JSON.stringify(data)
  }
  const response = await fetch(path, options)
  const result: unknown = await response.json().catch(() => null)
  if (!response.ok || result === null) {
    const code = typeof result === 'object' && result !== null && 'code' in result && typeof result.code === 'string'
      ? result.code : undefined
    throw new AdminContentError(response.ok ? 503 : response.status, code)
  }
  return result as T
}

export function listCollections(offset = 0) {
  return administrationRequest<ContentPage<CollectionChoice>>(`${collectionsPath}?offset=${offset}`)
}

export function listDrafts(type: string, offset = 0) {
  return administrationRequest<ContentPage<DraftSummary>>(`${collectionPath(type)}?offset=${offset}`)
}

export function createDraft(type: string, schemaNo: string, csrf: string) {
  return administrationRequest<DraftSummary>(collectionPath(type), csrf, 'POST', { schema_no: schemaNo })
}

export async function readDraft(type: string, itemID: string) {
  const path = draftPath(type, itemID)
  const [draft, metadata] = await Promise.all([
    administrationRequest<EditableDraft>(path),
    administrationRequest<EditorMetadata>(`${path}/schema`),
  ])
  if (draft.item_id !== itemID || metadata.item_id !== itemID || draft.schema_no !== metadata.schema_no || draft.version !== metadata.version) {
    throw new AdminContentError(409)
  }
  return { draft, metadata }
}

export function saveDraft(type: string, draft: EditableDraft, csrf: string, patch: ContentPatch) {
  return administrationRequest<EditableDraft>(draftPath(type, draft.item_id), csrf, 'PATCH', {
    expected_version: draft.version,
    schema_no: draft.schema_no,
    ...patch,
  })
}

export function commandDraft(
  type: string,
  draft: EditableDraft,
  action: DraftCommand,
  csrf: string,
  input: { locale?: string, intended_locales?: string[], schema_no?: string } = {},
) {
  return administrationRequest<DraftSummary>(`${draftPath(type, draft.item_id)}/${action}`, csrf, 'POST', {
    expected_version: draft.version,
    schema_no: draft.schema_no,
    ...input,
  })
}
