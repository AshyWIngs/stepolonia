import { computed, ref } from 'vue'
import { changeCollectionFields, type ManagedCollection, type RegistryField } from '~/utils/admin-content'
import { ownValue } from '~/utils/draft-edit-buffer'

interface BuilderRow {
  id: number
  field: RegistryField
  locked: boolean
  originalLabels?: NonNullable<RegistryField['display_labels']>
}

function labelsChanged({ field, originalLabels }: BuilderRow) {
  const current = field.display_labels
  if (current === originalLabels) return false
  const entries = Object.entries(current ?? {})
  return entries.length !== Object.keys(originalLabels ?? {}).length || entries.some(([locale, { label, help }]) => {
    const original = ownValue(originalLabels, locale)
    return label !== original?.label || help !== original?.help
  })
}

export function useRegistryFields() {
  const rows = ref<BuilderRow[]>([])
  const initialize = ref(false)
  let nextRowID = 0

  const hasNewFields = computed(() => rows.value.some(row => !row.locked))
  const visibleRows = computed(() => rows.value.filter(({ field }) =>
    field.key !== 'slug' || field.config?.slug !== true,
  ))
  const dirty = computed(() => initialize.value || hasNewFields.value || rows.value.some(labelsChanged))

  function replace(list: RegistryField[], keepDetails = false) {
    const previousIDs = new Map(keepDetails ? rows.value.map(row => [row.field.key, row.id]) : [])
    rows.value = list.map(field => ({
      id: previousIDs.get(field.key) ?? nextRowID++,
      locked: true,
      originalLabels: field.display_labels,
      field: {
        key: field.key,
        kind: field.kind,
        scope: field.scope,
        visibility: field.visibility,
        required: field.required,
        target_type_id: field.target_type_id,
        config: field.config,
        display_labels: field.display_labels,
      },
    }))
    initialize.value = false
  }

  function add() {
    const id = nextRowID++
    rows.value.push({ id, locked: false, field: { key: '', kind: 'string', scope: 'localized', visibility: 'private', required: false } })
    return id
  }

  function remove(id: number, existingCollection: boolean) {
    rows.value = rows.value.filter(row => row.id !== id)
    if (existingCollection && !hasNewFields.value) initialize.value = false
  }

  function saveChanges(current: ManagedCollection, csrf: string) {
    const additions = rows.value.filter(row => !row.locked).map(row => row.field)
    const labels = new Map(rows.value.filter(row => row.locked && labelsChanged(row)).map(row => [row.field.key, row.field.display_labels]))
    const updates = current.fields.filter(field => labels.has(field.key)).map(field => ({
      key: field.key,
      expected_version: field.label_version,
      display_labels: labels.get(field.key) ?? {},
    }))
    return changeCollectionFields(current, additions, updates, initialize.value, csrf)
  }

  return { rows, initialize, hasNewFields, visibleRows, dirty, replace, add, remove, saveChanges }
}
