import { computed, ref, type Ref } from 'vue'
import { AdminSettingsError, getPortalSettings, saveScopedPortalSetting } from '~/utils/admin-site-settings'
import type { EditablePortal, ScopedSettingsInput, ScopedSettingsSection } from '~/utils/admin-site-settings'
import type { PortalCatalogDrafts } from './PortalCatalogDrafts'
import type { PortalSiteDrafts } from './PortalSiteDrafts'
import type { PortalSlugRuleDraft } from './PortalSlugRuleDraft'

type SaveResult = Awaited<ReturnType<typeof saveScopedPortalSetting>>

const validationMessages: Record<ScopedSettingsSection, string> = {
  details: 'admin.settings_details_invalid',
  appearance: 'admin.settings_accent_invalid',
  'language-flags': 'admin.language_flag_invalid',
  'locale-defaults': 'admin.settings_locale_defaults_invalid',
  'slug-rules': 'admin.slug_rules_invalid',
  catalogs: 'admin.settings_catalog_invalid',
}

interface PublicationOptions {
  settings: Ref<EditablePortal | null>
  catalogs: PortalCatalogDrafts
  site: PortalSiteDrafts
  slugRules: PortalSlugRuleDraft
  savedValueDifferences: Ref<Map<string, string[]>>
  section: ScopedSettingsSection
  csrf: () => string
  isActive: () => boolean
  otherSaveInProgress: () => boolean
  translate: (key: string) => string
  started: () => void
  published: () => void
  sessionChanged: () => void
}

export class ScopedPortalPublication {
  readonly busy = ref(false)
  readonly notice = ref('')
  readonly failure = ref('')
  readonly pendingDraftConflict = ref(false)
  readonly reportedScope: Ref<ScopedSettingsSection>
  private readonly conflictedScopes = ref(new Set<string>())
  private readonly rateLimitedScopes = ref(new Set<string>())
  private readonly retryTimers = new Map<string, ReturnType<typeof setTimeout>>()
  readonly hasReportedConflict = computed(() => this.hasConflict(this.reportedScope.value))

  constructor(private readonly options: PublicationOptions) {
    this.reportedScope = ref(options.section)
  }

  private catalogCode() {
    const { catalogs } = this.options
    return catalogs.catalogTarget.value || catalogs.selectedLanguage.value
  }

  private scopeKey(scope: ScopedSettingsSection) {
    if (scope === 'language-flags') return `flag:${this.options.site.flagLanguage.value}`
    if (scope === 'catalogs') return `catalog:${this.catalogCode()}`
    return scope
  }

  selectScope(scope: ScopedSettingsSection) {
    this.reportedScope.value = scope
    this.options.catalogs.catalogTarget.value = ''
    this.notice.value = ''
    this.failure.value = ''
  }

  hasConflict(scope: ScopedSettingsSection) {
    return this.conflictedScopes.value.has(this.scopeKey(scope))
  }

  saveBlocked(scope: ScopedSettingsSection) {
    return this.options.otherSaveInProgress() || this.busy.value || this.pendingDraftConflict.value || this.options.settings.value?.has_draft ||
      this.hasConflict(scope) || this.rateLimitedScopes.value.has(this.scopeKey(scope))
  }

  feedback(scope: ScopedSettingsSection) {
    const { settings, translate } = this.options
    const reported = this.reportedScope.value === scope
    const conflicted = this.hasConflict(scope)
    const error = (reported && this.failure.value) || this.blockingMessage(scope, conflicted)
    return {
      scope,
      status: reported && this.notice.value ? translate(this.notice.value) : '',
      error: error ? translate(error) : '',
      reload: conflicted && !this.pendingDraftConflict.value && !settings.value?.has_draft ? translate('admin.settings_scoped_reload') : '',
      review: translate('admin.settings_review_saved_values'),
      values: reported ? this.changedSavedValues() : [],
      disabled: this.busy.value,
    }
  }

  private changedSavedValues() {
    const { catalogs, savedValueDifferences } = this.options
    return catalogs.registryKeysReloaded.value
      ? [...savedValueDifferences.value].filter(([scope]) => scope.startsWith('catalog:')).flatMap(([, values]) => values)
      : savedValueDifferences.value.get(this.scopeKey(this.reportedScope.value)) ?? []
  }

  private blockingMessage(scope: ScopedSettingsSection, conflicted: boolean) {
    if (conflicted) return 'admin.settings_conflict'
    return this.rateLimitedScopes.value.has(this.scopeKey(scope)) ? 'admin.limited' : ''
  }

  async save(scope: ScopedSettingsSection, prepare: (editable: EditablePortal, code: string) => Promise<boolean>) {
    const editable = this.options.settings.value
    if (!editable || this.saveBlocked(scope)) return false
    this.reportedScope.value = scope
    const code = scope === 'catalogs' ? this.catalogCode() : this.options.site.flagLanguage.value
    if (!await prepare(editable, code)) return false
    const input = this.input(editable, scope, code)
    if (!input) return false
    this.busy.value = true
    this.options.started()
    this.notice.value = ''
    this.failure.value = ''
    try {
      const result = await saveScopedPortalSetting(scope, this.options.csrf(), input)
      if (!this.options.isActive()) return false
      this.commit(editable, scope, code, result)
      this.notice.value = 'admin.settings_saved'
      this.options.published()
    } catch (error: unknown) {
      if (this.options.isActive()) this.handleFailure(error)
    } finally {
      this.busy.value = false
    }
    return true
  }

  private input(editable: EditablePortal, scope: ScopedSettingsSection, code: string): ScopedSettingsInput | undefined {
    if (scope === 'slug-rules') return this.options.slugRules.input(editable)
    if (scope === 'catalogs') return this.options.catalogs.catalogInput(editable, code)
    return this.options.site.input(editable, scope, code)
  }

  private commit(editable: EditablePortal, scope: ScopedSettingsSection, code: string, result: SaveResult) {
    if (result.previousVersion === editable.version) editable.version = result.version
    editable.published_id = result.publicationID
    this.options.savedValueDifferences.value.delete(this.scopeKey(scope))
    if (scope === 'slug-rules') this.options.slugRules.commit(editable, result.scopeVersion)
    else if (scope === 'catalogs') this.options.catalogs.commitCatalog(editable, code, result.scopeVersion)
    else this.options.site.commit(editable, scope, code, result)
  }

  async refresh(scope: ScopedSettingsSection) {
    const editable = this.options.settings.value
    if (!editable || this.busy.value) return false
    this.reportedScope.value = scope
    this.busy.value = true
    try {
      const fresh = await getPortalSettings()
      if (!this.options.isActive()) return false
      editable.has_draft = fresh.has_draft
      if (fresh.has_draft) {
        this.pendingDraftConflict.value = true
        return false
      }
      this.rebase(editable, fresh, scope)
      this.conflictedScopes.value.delete(this.scopeKey(scope))
      this.failure.value = ''
    } catch (error: unknown) {
      if (this.options.isActive()) this.handleFailure(error)
    } finally {
      this.busy.value = false
    }
    return true
  }

  async reloadCatalogRegistry(syncDrafts: (editable: EditablePortal) => void) {
    const { settings, catalogs, isActive, otherSaveInProgress } = this.options
    const current = settings.value
    if (!current || this.busy.value || otherSaveInProgress()) return
    this.busy.value = true
    try {
      const fresh = await getPortalSettings()
      if (!isActive()) return
      if (fresh.has_draft) {
        this.pendingDraftConflict.value = true
        return
      }
      catalogs.rebaseCatalogRegistry(current, fresh)
      syncDrafts(fresh)
      settings.value = fresh
      catalogs.registryKeysReloaded.value = true
      this.failure.value = ''
    } catch (error: unknown) {
      if (isActive()) this.handleFailure(error)
    } finally {
      this.busy.value = false
    }
  }

  private rebase(editable: EditablePortal, fresh: EditablePortal, scope: ScopedSettingsSection) {
    if (scope === 'slug-rules') this.options.slugRules.rebase(editable, fresh)
    else if (scope === 'catalogs') this.options.catalogs.rebaseCatalog(editable, fresh, this.catalogCode())
    else this.options.site.rebase(editable, fresh, scope)
  }

  private handleFailure(error: unknown) {
    if (!(error instanceof AdminSettingsError)) {
      this.failure.value = 'admin.unavailable'
      return
    }
    if (error.status === 429) {
      this.startCooldown(error.retryAfter ?? 1)
      return
    }
    if (error.status === 401 || error.code === 'csrf_failed') {
      this.failure.value = 'admin.changed'
      this.options.sessionChanged()
    } else if (error.code === 'invalid_language_flag') {
      this.failure.value = 'admin.language_flag_invalid'
    } else if (error.code === 'site_draft_pending') {
      this.pendingDraftConflict.value = true
    } else if (error.code === 'invalid_slug_rules') {
      this.failure.value = 'admin.slug_rules_invalid'
    } else if (error.code === 'catalog_registry_changed') {
      this.options.catalogs.registryRepairRequired.value = true
      this.options.catalogs.registryKeysReloaded.value = false
      this.failure.value = 'admin.settings_catalog_registry_changed'
    } else if (error.status === 409) {
      this.conflictedScopes.value.add(this.scopeKey(this.reportedScope.value))
    } else if (error.status === 400) {
      this.failure.value = validationMessages[this.reportedScope.value]
    } else if (error.status === 403) {
      this.failure.value = 'admin.access_denied'
    } else {
      this.failure.value = 'admin.unavailable'
    }
  }

  private startCooldown(seconds: number) {
    const key = this.scopeKey(this.reportedScope.value)
    this.failure.value = 'admin.limited'
    this.rateLimitedScopes.value.add(key)
    clearTimeout(this.retryTimers.get(key))
    this.retryTimers.set(key, setTimeout(() => {
      this.retryTimers.delete(key)
      this.rateLimitedScopes.value.delete(key)
      if (key === this.scopeKey(this.reportedScope.value) && this.failure.value === 'admin.limited') this.failure.value = ''
    }, Math.min(seconds, 2147483) * 1000))
  }

  dispose() {
    for (const timer of this.retryTimers.values()) clearTimeout(timer)
  }
}
