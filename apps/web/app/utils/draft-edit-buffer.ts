import type { ContentPatch, EditableDraft, EditorField, Scalar } from './admin-content'

export interface FieldEdit {
  field: EditorField
  locale: string
  value: Scalar | undefined
}

export function savedFieldValue(draft: EditableDraft, field: EditorField, locale: string): Scalar | undefined {
  if (field.kind === 'relation') {
    return draft.relations.find(item => item.field_id === field.field_id && item.locale === locale)?.target_item_id
  }
  const values = locale === '' ? draft.shared : ownValue(draft.locales, locale)?.values
  return ownValue(values, field.key)
}

export function ownValue<T>(values: Readonly<Record<string, T>> | undefined, key: string): T | undefined {
  return Object.entries(values ?? {}).find(([entry]) => entry === key)?.[1]
}

export function draftPatch(edits: Iterable<FieldEdit>): ContentPatch {
  const locales = new Map<string, NonNullable<ContentPatch['locales']>[string]>()
  const shared = new Map<string, { kind: EditorField['kind'], value: Scalar }>()
  const unsetShared: string[] = []
  const relationChanges: NonNullable<ContentPatch['relation_changes']> = []
  for (const { field, locale, value } of edits) {
    if (field.kind === 'relation') {
      relationChanges.push({
        field_id: field.field_id,
        locale: locale || undefined,
        target_item_id: typeof value === 'string' ? value : undefined,
      })
      locales.set(locale, locales.get(locale) ?? {})
      continue
    }
    if (locale === '') {
      if (value === undefined) unsetShared.push(field.key)
      else shared.set(field.key, { kind: field.kind, value })
      continue
    }
    locales.set(locale, updateLocalePatch(locales.get(locale) ?? {}, field, value))
  }
  locales.delete('')
  return {
    shared: Object.fromEntries(shared),
    unset_shared: unsetShared,
    locales: Object.fromEntries(locales),
    relation_changes: relationChanges,
  }
}

function updateLocalePatch(patch: NonNullable<ContentPatch['locales']>[string], field: EditorField, value: Scalar | undefined) {
  if (value === undefined) {
    patch.unset ??= []
    patch.unset.push(field.key)
  }
  else {
    patch.values = { ...patch.values, [field.key]: { kind: field.kind, value } }
  }
  return patch
}
