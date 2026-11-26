import { computed, ref, type Ref } from 'vue'
import type { EditablePortal } from '~/utils/admin-site-settings'
import { changedTextValues } from './changedTextValues'

type Catalog = Record<string, string>

export class PortalCatalogDrafts {
  readonly selectedLanguage = ref('')
  readonly catalogTarget = ref('')
  readonly registryRepairRequired = ref(false)
  readonly registryKeysReloaded = ref(false)
  private readonly edits = ref(new Map<string, Catalog | null>())
  private readonly bases = ref(new Map<string, Catalog | undefined>())
  readonly editedCatalogCodes = computed(() => [...this.edits.value.keys()])
  readonly dirty = computed(() => this.edits.value.size > 0)
  readonly catalogsByLanguage = computed(() => {
    const catalogs = new Map(Object.entries(this.editableSettings.value?.catalogs ?? {}))
    for (const [code, entries] of this.edits.value) {
      if (entries) catalogs.set(code, entries)
      else catalogs.delete(code)
    }
    return catalogs
  })
  readonly messages = computed(() => this.catalogsByLanguage.value.get(this.selectedLanguage.value))
  readonly reference = computed(() => this.catalogsByLanguage.value.get(this.editableSettings.value?.settings.fallback_language ?? ''))
  readonly catalogLanguages = computed(() => {
    const editable = this.editableSettings.value
    return editable?.languages.filter(item => Object.hasOwn(editable.catalogs, item.code)) ?? []
  })
  readonly disabledCatalogCodes = computed(() => {
    const editable = this.editableSettings.value
    if (!editable) return []
    const enabled = new Set(editable.languages.map(item => item.code))
    return [...this.catalogsByLanguage.value.keys()].filter(code => !enabled.has(code) && code !== editable.settings.fallback_language)
  })

  private base(editable: EditablePortal, code: string) {
    return this.bases.value.has(code) ? this.bases.value.get(code) : editable.catalogs[code]
  }

  private stage(editable: EditablePortal, code: string, values: Catalog | null) {
    if (!this.bases.value.has(code)) this.bases.value.set(code, editable.catalogs[code])
    this.edits.value.set(code, values)
  }

  private clearEdit(code: string) {
    this.edits.value.delete(code)
    this.bases.value.delete(code)
  }

  clearCatalogEdits() {
    this.edits.value.clear()
    this.bases.value.clear()
  }

  addCatalog() {
    const editable = this.editableSettings.value
    const code = this.selectedLanguage.value
    if (!editable?.languages.some(item => item.code === code) || this.messages.value) return
    this.stage(editable, code, {})
  }

  removeCatalog(code: string) {
    const editable = this.editableSettings.value
    if (!editable || code === editable.settings.fallback_language) return
    if (!Object.hasOwn(editable.catalogs, code)) {
      this.clearEdit(code)
      return
    }
    this.stage(editable, code, null)
    this.catalogTarget.value = code
  }

  changeMessage(key: string, value: string) {
    const editable = this.editableSettings.value
    const entries = this.messages.value
    if (!editable || !entries || !editable.message_keys.includes(key)) return
    const updated = new Map(Object.entries(entries))
    updated.set(key, value)
    const code = this.selectedLanguage.value
    const next = Object.fromEntries(updated)
    if (Object.keys(changedTextValues(this.base(editable, code), next)).length > 0) this.stage(editable, code, next)
    else this.clearEdit(code)
  }

  catalogInput(editable: EditablePortal, code: string) {
    const edit = this.edits.value.get(code)
    if (edit === undefined) return
    const target = { expected_version: editable.catalog_versions[code] ?? '', language_code: code }
    if (edit === null) return { ...target, remove_catalog: true as const }
    const messages = changedTextValues(this.base(editable, code), edit)
    if (Object.keys(messages).length > 0) return { ...target, messages }
    this.clearEdit(code)
  }

  catalogCompleteness(editable: EditablePortal, code: string) {
    const edit = this.edits.value.get(code)
    if (edit === null) return 'complete'
    if (!edit) return 'absent'
    return editable.message_keys.every(key => edit[key]?.trim()) ? 'complete' : 'incomplete'
  }

  commitCatalog(editable: EditablePortal, code: string, version: string) {
    const catalogs = new Map(Object.entries(editable.catalogs))
    const entries = this.edits.value.get(code)
    if (entries) catalogs.set(code, { ...editable.catalogs[code], ...changedTextValues(this.base(editable, code), entries) })
    else catalogs.delete(code)
    editable.catalogs = Object.fromEntries(catalogs)
    editable.catalog_versions[code] = version
    this.clearEdit(code)
    this.catalogTarget.value = ''
  }

  private showDifferences(before: Catalog | undefined, actual: Catalog | undefined, keys: string[], code: string) {
    const differences = keys.filter(key => before?.[key] !== actual?.[key]).map(key =>
      `${this.localeLabel(code)} · ${actual?.[key] ?? this.translate('admin.settings_removed_value')}`,
    )
    this.recordSavedValues(`catalog:${code}`, differences)
  }

  rebaseCatalog(editable: EditablePortal, fresh: EditablePortal, code: string) {
    const previous = this.base(editable, code)
    const actual = fresh.catalogs[code]
    const pending = this.edits.value.get(code)
    if (pending) {
      const delta = changedTextValues(previous, pending)
      this.showDifferences(previous, actual, Object.keys(delta), code)
      if (Object.keys(changedTextValues(actual, delta)).length > 0) {
        this.edits.value.set(code, { ...actual, ...delta })
        this.bases.value.set(code, actual)
      } else this.clearEdit(code)
    } else if (pending === null && actual) {
      this.showDifferences(previous, actual, Object.keys({ ...previous, ...actual }), code)
      this.bases.value.set(code, actual)
    } else if (!actual) {
      this.clearEdit(code)
    }
    const catalogs = new Map(Object.entries(editable.catalogs))
    if (actual) catalogs.set(code, actual)
    else catalogs.delete(code)
    editable.catalogs = Object.fromEntries(catalogs)
    editable.catalog_versions[code] = fresh.catalog_versions[code] ?? ''
  }

  preserveCatalogVersions(fresh: EditablePortal) {
    const current = this.editableSettings.value
    if (!current) return
    for (const code of this.edits.value.keys()) {
      const version = current.catalog_versions[code]
      if (version) fresh.catalog_versions[code] = version
    }
  }

  rebaseCatalogRegistry(current: EditablePortal, fresh: EditablePortal) {
    for (const code of this.edits.value.keys()) {
      this.rebaseCatalog(current, fresh, code)
      const entries = this.edits.value.get(code)
      if (entries) this.edits.value.set(code, Object.fromEntries(Object.entries(entries).filter(([key]) => fresh.message_keys.includes(key))))
    }
  }

  hasCatalogEdit(code: string) {
    return this.edits.value.has(code)
  }

  constructor(
    private readonly editableSettings: Ref<EditablePortal | null>,
    private readonly localeLabel: (code: string) => string,
    private readonly translate: (key: string) => string,
    private readonly recordSavedValues: (scope: string, values: string[]) => void,
  ) {}
}

export function incompleteCatalog(catalogs: EditablePortal['catalogs'], keys: string[]): string | undefined {
  return Object.entries(catalogs).find(([, entries]) => {
    const nonBlankKeys = new Set(Object.entries(entries).filter(([, text]) => text.trim()).map(([key]) => key))
    return keys.some(key => !nonBlankKeys.has(key))
  })?.[0]
}
