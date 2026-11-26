import { computed, ref } from 'vue'
import type { AdminEntryCatalog, AdminWorkspaceCatalog, useAdminUI } from '~/composables/useAdminUI'
import { AdminContentError } from '~/utils/admin-request'

type CatalogContext = {
  isActive: () => boolean
  workspaceOpen: () => boolean
  sessionPending: () => boolean
  changeLocale: (catalog: AdminEntryCatalog) => void
  rememberLanguage: (language: string) => void
  refreshChangedAccount: (language: string) => Promise<void>
  sessionChanged: () => Promise<void>
}

export class AdminCatalogs {
  private readonly entryCatalog
  readonly workspaceCatalog = ref<AdminWorkspaceCatalog>()
  readonly catalog = computed(() => this.workspaceCatalog.value ?? this.entryCatalog.value)
  readonly changingLanguage = ref(false)
  readonly catalogFailure = ref(false)
  private catalogRequest = 0
  private languageChoice: string | undefined

  constructor(
    initialCatalog: AdminEntryCatalog,
    private readonly source: ReturnType<typeof useAdminUI>,
    private readonly context: CatalogContext,
  ) {
    this.entryCatalog = ref(initialCatalog)
  }

  private currentRequest(requestID: number) {
    return this.context.isActive() && requestID === this.catalogRequest
  }

  clearWorkspace() {
    ++this.catalogRequest
    this.languageChoice = undefined
    if (!this.workspaceCatalog.value) return
    this.workspaceCatalog.value = undefined
    this.context.changeLocale(this.entryCatalog.value)
  }

  changedWorkspaceAccount(next?: AdminWorkspaceCatalog) {
    return next && this.workspaceCatalog.value && this.workspaceCatalog.value.account_id !== next.account_id
  }

  async openWorkspace(language: string, activate: (next: AdminWorkspaceCatalog) => Promise<void>) {
    const requestID = ++this.catalogRequest
    this.languageChoice = undefined
    const next = await this.source.readWorkspace(language)
    if (this.currentRequest(requestID)) await activate(next)
  }

  activateWorkspace(next: AdminWorkspaceCatalog) {
    this.workspaceCatalog.value = next
    this.context.changeLocale(next)
    this.catalogFailure.value = false
  }

  async update(language: string, remember: boolean) {
    const requestID = ++this.catalogRequest
    this.catalogFailure.value = false
    try {
      const result = this.context.workspaceOpen()
        ? { workspace: await this.source.readWorkspace(language) }
        : { entry: await this.source.readEntry(language) }
      if (!this.currentRequest(requestID)) return
      if (this.changedWorkspaceAccount(result.workspace)) {
        await this.context.refreshChangedAccount(language)
        return
      }
      if (result.workspace) this.workspaceCatalog.value = result.workspace
      else this.entryCatalog.value = result.entry
      const next = result.workspace ?? result.entry
      this.context.changeLocale(next)
      if (remember && next.language.code === language) this.context.rememberLanguage(language)
    }
    catch (error: unknown) {
      if (!this.currentRequest(requestID)) return
      if (error instanceof AdminContentError && error.status === 401) await this.context.sessionChanged()
      else this.catalogFailure.value = true
    }
    finally {
      if (requestID === this.catalogRequest) this.languageChoice = undefined
    }
  }

  selectLanguage = async (language: string) => {
    if (this.context.sessionPending() || this.changingLanguage.value || !this.catalog.value.languages.some(choice => choice.code === language)) return
    this.changingLanguage.value = true
    this.languageChoice = language
    try { await this.update(language, true) }
    finally { this.changingLanguage.value = false }
  }

  refreshPublished(language: string) {
    return this.update(this.languageChoice ?? language, this.languageChoice !== undefined)
  }
}
