import { ref, type Ref } from 'vue'
import { AdminSettingsError, getPortalSettings, savePortalSettings } from '~/utils/admin-site-settings'
import type { EditablePortal } from '~/utils/admin-site-settings'
import type { PortalCatalogDrafts } from './PortalCatalogDrafts'
import type { ScopedPortalPublication } from './ScopedPortalPublication'

interface RevisionOptions {
  settings: Ref<EditablePortal | null>
  catalogs: PortalCatalogDrafts
  scoped: ScopedPortalPublication
  csrf: () => string
  uiLanguage: () => string
  isActive: () => boolean
  syncDrafts: (editable: EditablePortal) => void
  validate: (editable: EditablePortal, catalogs: EditablePortal['catalogs']) => Promise<boolean>
  invalidFlag: () => void
  stepUp: () => void
  sessionChanged: () => void
  published: () => void
}

// Applies a pending revision or the complete catalog after a registry repair.
export class PortalRevisionPublication {
  readonly busy = ref(false)
  readonly conflict = ref(false)
  readonly failure = ref('')
  readonly notice = ref('')

  constructor(private readonly options: RevisionOptions) {}

  async load() {
    const { settings, catalogs, scoped, isActive, uiLanguage, syncDrafts } = this.options
    if (this.busy.value || scoped.busy.value) return
    this.busy.value = true
    this.failure.value = ''
    this.notice.value = ''
    try {
      const editable = await getPortalSettings()
      if (!isActive()) return
      syncDrafts(editable)
      settings.value = editable
      scoped.pendingDraftConflict.value = false
      const locale = uiLanguage()
      catalogs.selectedLanguage.value = editable.languages.some(item => item.code === locale)
        ? locale : editable.settings.fallback_language
      catalogs.clearCatalogEdits()
      this.conflict.value = false
    } catch (error: unknown) {
      if (isActive()) this.handleFailure(error)
    } finally {
      this.busy.value = false
    }
  }

  async publish() {
    const { settings, catalogs, scoped, isActive, csrf, validate, published } = this.options
    const editable = settings.value
    if (!editable || this.busy.value || scoped.busy.value || this.conflict.value ||
      scoped.pendingDraftConflict.value || (!catalogs.registryKeysReloaded.value && !editable.has_draft)) return false
    const pendingCatalogs = Object.fromEntries(catalogs.catalogsByLanguage.value)
    if (!await validate(editable, pendingCatalogs)) return false
    this.busy.value = true
    this.failure.value = ''
    this.notice.value = ''
    scoped.notice.value = ''
    try {
      const result = await savePortalSettings(csrf(), {
        expected_version: editable.version,
        settings: editable.settings,
        catalogs: pendingCatalogs,
      })
      if (!isActive()) return false
      editable.version = result.version
      editable.published_id = result.publicationID
      editable.has_draft = false
      editable.catalogs = pendingCatalogs
      editable.language_flags = { ...editable.language_flags, ...result.languageFlags }
      catalogs.clearCatalogEdits()
      await this.refreshVersions(result.publicationID)
      catalogs.registryKeysReloaded.value = false
      catalogs.registryRepairRequired.value = false
      this.notice.value = 'admin.settings_saved'
      published()
    } catch (error: unknown) {
      if (isActive()) this.handleFailure(error)
    } finally {
      this.busy.value = false
    }
    return isActive() && this.notice.value === 'admin.settings_saved'
  }

  private async refreshVersions(publicationID: string) {
    const { settings, catalogs, isActive, syncDrafts } = this.options
    const fresh = await getPortalSettings().catch(() => undefined)
    if (!isActive() || fresh?.published_id !== publicationID || !settings.value) return
    syncDrafts(fresh)
    catalogs.preserveCatalogVersions(fresh)
    settings.value = fresh
  }

  handleFailure(error: unknown) {
    if (!(error instanceof AdminSettingsError)) {
      this.failure.value = 'admin.unavailable'
      return
    }
    if (error.code === 'step_up_required') {
      this.options.stepUp()
      return
    }
    if (error.code === 'invalid_language_flag') {
      this.failure.value = 'admin.language_flag_invalid'
      this.options.invalidFlag()
      return
    }
    if (error.status === 401 || error.code === 'csrf_failed') {
      this.options.sessionChanged()
      this.failure.value = 'admin.changed'
      return
    }
    this.reportStatus(error.status)
  }

  private reportStatus(status: number) {
    switch (status) {
      case 400:
      case 413:
        this.failure.value = 'admin.settings_invalid'
        break
      case 403:
        this.options.settings.value = null
        this.options.catalogs.clearCatalogEdits()
        this.failure.value = 'admin.access_denied'
        break
      case 409:
        this.conflict.value = true
        this.failure.value = 'admin.settings_conflict'
        break
      case 429:
        this.failure.value = 'admin.limited'
        break
      default:
        this.failure.value = 'admin.unavailable'
    }
  }
}
