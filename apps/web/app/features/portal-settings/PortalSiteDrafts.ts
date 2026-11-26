import { computed, ref, type Ref } from 'vue'
import type { EditablePortal, PortalDetails, PortalFields, PortalLocaleDefaults, ScopedSettingsSection, saveScopedPortalSetting } from '~/utils/admin-site-settings'
import { changedTextValues } from './changedTextValues'

type SiteScope = Exclude<ScopedSettingsSection, 'slug-rules' | 'catalogs'>
type SaveResult = Awaited<ReturnType<typeof saveScopedPortalSetting>>

const accentPattern = /^#[\da-f]{6}$/iu

export function isPortalAccent(value: string) {
  return accentPattern.test(value)
}

function portalDetails({ title, tagline, description, notice }: PortalFields): PortalDetails {
  return { title, tagline, description, notice }
}

function portalLocaleDefaults({ default_language, fallback_language }: PortalFields): PortalLocaleDefaults {
  return { default_language, fallback_language }
}

export class PortalSiteDrafts {
  readonly detailsDraft = ref<PortalDetails>()
  readonly localeDefaultsDraft = ref<PortalLocaleDefaults>()
  readonly flagUpdates = ref<Record<string, string>>({})
  readonly flagLanguage = ref('')
  readonly accentDraft = ref('')
  private readonly detailsBase = ref<PortalDetails>()
  private readonly localeDefaultsBase = ref<PortalLocaleDefaults>()
  readonly detailsDirty = computed(() => JSON.stringify(this.detailsDraft.value) !== JSON.stringify(this.detailsBase.value))
  readonly localeDefaultsDirty = computed(() => JSON.stringify(this.localeDefaultsDraft.value) !== JSON.stringify(this.localeDefaultsBase.value))
  readonly dirty = computed(() => this.detailsDirty.value || this.localeDefaultsDirty.value ||
    this.accentDraft.value !== (this.editableSettings.value?.settings.accent ?? '') || Object.keys(this.flagUpdates.value).length > 0)
  readonly pickerColor = computed(() => isPortalAccent(this.accentDraft.value) ? this.accentDraft.value : undefined)

  constructor(
    private readonly editableSettings: Ref<EditablePortal | null>,
    private readonly removedValueLabel: () => string,
    private readonly recordSavedValues: (scope: string, values: string[]) => void,
  ) {}

  sync(editable: EditablePortal) {
    const previous = this.editableSettings.value
    if (previous && this.detailsDirty.value) editable.details_version = previous.details_version
    else {
      this.detailsBase.value = portalDetails(editable.settings)
      this.detailsDraft.value = { ...this.detailsBase.value }
    }
    if (previous && this.localeDefaultsDirty.value) editable.locale_defaults_version = previous.locale_defaults_version
    else {
      this.localeDefaultsBase.value = portalLocaleDefaults(editable.settings)
      this.localeDefaultsDraft.value = { ...this.localeDefaultsBase.value }
    }
    for (const code of Object.keys(this.flagUpdates.value)) {
      const version = previous?.language_flag_versions[code]
      if (version) editable.language_flag_versions[code] = version
    }
    if (!previous || this.accentDraft.value === previous.settings.accent) this.accentDraft.value = editable.settings.accent
    else editable.appearance_version = previous.appearance_version
  }

  input(editable: EditablePortal, scope: SiteScope, code: string) {
    if (scope === 'details') return this.detailsDraft.value && { expected_version: editable.details_version, details: this.detailsDraft.value }
    if (scope === 'appearance') return { expected_version: editable.appearance_version, accent: this.accentDraft.value }
    if (scope === 'locale-defaults') return this.localeDefaultsDraft.value && { expected_version: editable.locale_defaults_version, defaults: this.localeDefaultsDraft.value }
    const svg = this.flagUpdates.value[code]
    if (svg !== undefined) return { expected_version: editable.language_flag_versions[code] ?? '', language_code: code, svg }
  }

  commit(editable: EditablePortal, scope: SiteScope, code: string, result: SaveResult) {
    switch (scope) {
      case 'details':
        Object.assign(editable.settings, this.detailsDraft.value)
        this.detailsBase.value = portalDetails(editable.settings)
        editable.details_version = result.scopeVersion
        break
      case 'locale-defaults':
        Object.assign(editable.settings, this.localeDefaultsDraft.value)
        this.localeDefaultsBase.value = portalLocaleDefaults(editable.settings)
        editable.locale_defaults_version = result.scopeVersion
        break
      case 'appearance':
        editable.settings.accent = result.value
        editable.appearance_version = result.scopeVersion
        this.accentDraft.value = result.value
        break
      case 'language-flags':
        editable.language_flags = { ...editable.language_flags, [code]: result.value }
        editable.language_flag_versions[code] = result.scopeVersion
        this.flagUpdates.value = Object.fromEntries(Object.entries(this.flagUpdates.value).filter(([language]) => language !== code))
        break
    }
  }

  rebase(editable: EditablePortal, fresh: EditablePortal, scope: SiteScope) {
    switch (scope) {
      case 'details': {
        const details = portalDetails(fresh.settings)
        this.detailsDraft.value = this.rebaseFields(scope, this.detailsBase.value, this.detailsDraft.value, details)
        this.detailsBase.value = details
        Object.assign(editable.settings, details)
        editable.details_version = fresh.details_version
        break
      }
      case 'locale-defaults': {
        const defaults = portalLocaleDefaults(fresh.settings)
        this.localeDefaultsDraft.value = this.rebaseFields(scope, this.localeDefaultsBase.value, this.localeDefaultsDraft.value, defaults)
        this.localeDefaultsBase.value = defaults
        Object.assign(editable.settings, defaults)
        editable.locale_defaults_version = fresh.locale_defaults_version
        break
      }
      case 'appearance':
        editable.appearance_version = fresh.appearance_version
        editable.settings.accent = fresh.settings.accent
        break
      case 'language-flags': {
        const code = this.flagLanguage.value
        editable.language_flags = { ...editable.language_flags, [code]: fresh.language_flags?.[code] ?? '' }
        editable.language_flag_versions[code] = fresh.language_flag_versions[code] ?? ''
        break
      }
    }
  }

  private rebaseFields<T extends Record<string, string>>(scope: SiteScope, before: T | undefined, pending: T | undefined, fresh: T): T {
    const edits = pending ? changedTextValues(before, pending) : {}
    const overlapping = Object.keys(edits).filter(key => before?.[key] !== fresh[key])
    this.recordSavedValues(scope, overlapping.map(key => fresh[key] ?? this.removedValueLabel()))
    return { ...fresh, ...edits }
  }
}
