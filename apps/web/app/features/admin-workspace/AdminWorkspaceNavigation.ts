import type { useRoute, useRouter } from '#imports'
import { computed, ref } from 'vue'
import { listCollections, listRegistryRenderers, type CollectionChoice, type RegistryCollection } from '~/utils/admin-content'
import { AdminContentError } from '~/utils/admin-request'
import { portalSettingsPages, type PortalSettingsPage } from '~/utils/admin-site-settings'

export const domains = ['content', 'registry', 'settings', 'localization', 'access'] as const
export const accessPages = ['users', 'roles', 'audit', 'security', 'rate_limits'] as const
export const portalPages = Object.keys(portalSettingsPages) as PortalSettingsPage[]
export type AdminTool = typeof domains[number] | 'account' | 'signed_in'
type AccessPage = typeof accessPages[number]
const createCollectionView = 'new-collection'

export function isPortalTool(tool: AdminTool): tool is 'settings' | 'localization' {
  return tool === 'settings' || tool === 'localization'
}

export function hasSubpages(tool: AdminTool) {
  return isPortalTool(tool) || tool === 'access' || tool === 'content' || tool === 'registry'
}

export class AdminWorkspaceNavigation {
  readonly activeTool = ref<AdminTool>('signed_in')
  readonly expandedTool = ref<AdminTool>()
  readonly contentCollections = ref<CollectionChoice[]>([])
  readonly contentOffset = ref<number | null>(null)
  readonly contentSelection = ref('')
  readonly selectedContent = computed(() => this.contentCollections.value.find(entry => entry.type_key === this.contentSelection.value))
  readonly registry = ref<Awaited<ReturnType<typeof listRegistryRenderers>>>()
  readonly registrySelection = ref('')
  readonly registryCreating = ref(false)
  readonly registryForm = ref(0)
  readonly enabledCollections = computed(() => this.registry.value?.collections.filter(entry => entry.enabled) ?? [])
  readonly disabledCollections = computed(() => this.registry.value?.collections.filter(entry => !entry.enabled) ?? [])
  readonly collectionNavigationLoading = ref(false)
  readonly collectionNavigationError = ref('')
  readonly collectionPageError = ref('')
  private collectionNavigationRequest = 0
  readonly portalPage = ref<PortalSettingsPage>('identity')
  readonly permissionTexts = ref(false)
  readonly accessSection = ref<AccessPage>('users')
  readonly editingState = ref({ dirty: false, busy: false })
  readonly leaveDialog = ref<HTMLDialogElement>()
  private leaveAction: (() => void) | undefined

  constructor(
    private readonly route: ReturnType<typeof useRoute>,
    private readonly router: ReturnType<typeof useRouter>,
    private readonly isActive: () => boolean,
    private readonly locked: () => boolean,
    private readonly focusHeading: () => Promise<void>,
    private readonly clearFeedback: () => void,
    private readonly notifyRegistryDisabled: () => void,
    private readonly sessionChanged: () => Promise<void>,
  ) {}

  resetWorkspace() {
    this.activeTool.value = 'signed_in'
    this.expandedTool.value = undefined
    this.editingState.value = { dirty: false, busy: false }
  }

  clearLeaveAction = () => {
    this.leaveAction = undefined
  }

  resetSession() {
    this.leaveAction = undefined
    ++this.collectionNavigationRequest
    this.collectionNavigationLoading.value = false
    this.collectionNavigationError.value = ''
    this.collectionPageError.value = ''
    this.registry.value = undefined
    this.registrySelection.value = ''
    this.registryCreating.value = false
    this.contentSelection.value = ''
    this.contentCollections.value = []
    this.contentOffset.value = null
  }

  navigate = (action: () => void) => {
    if (this.locked()) return
    if (!this.editingState.value.dirty) {
      action()
      return
    }
    this.leaveAction = action
    this.leaveDialog.value?.showModal()
  }

  completeNavigation = () => {
    const action = this.leaveAction
    this.leaveAction = undefined
    this.leaveDialog.value?.close()
    action?.()
  }

  private workspaceView(tool: AdminTool) {
    if (isPortalTool(tool)) return this.portalPage.value
    if (tool === 'access') return this.accessSection.value
    if (tool === 'content') return this.contentSelection.value || undefined
    if (tool === 'registry') return this.registryCreating.value ? createCollectionView : this.registrySelection.value || undefined
    return undefined
  }

  private rememberWorkspace() {
    const section = this.activeTool.value === 'signed_in' ? undefined : this.activeTool.value
    const view = this.workspaceView(this.activeTool.value)
    const group = view === 'translations' && section === 'localization' && this.permissionTexts.value ? 'permissions' : undefined
    if (this.route.query.section === section && this.route.query.view === view && this.route.query.group === group) return
    void this.router.replace({ query: { ...this.route.query, section, view, group }, hash: this.route.hash })
  }

  restoreWorkspace() {
    const section = this.route.query.section
    const view = this.route.query.view
    this.permissionTexts.value = section === 'localization' && view === 'translations' && this.route.query.group === 'permissions'
    const tool = section === 'account' ? section : domains.find(entry => entry === section)
    if (!tool) {
      this.rememberWorkspace()
      return
    }
    if (isPortalTool(tool)) {
      const page = portalPages.find(entry => entry === view && portalSettingsPages[entry].section === tool)
      this.openPortalPage(page ?? (tool === 'settings' ? 'identity' : 'languages'), false)
      return
    }
    const collection = typeof view === 'string' && view.length <= 128 ? view : ''
    if (tool === 'access') this.accessSection.value = accessPages.find(page => page === view) ?? 'users'
    if (tool === 'content') this.contentSelection.value = collection
    if (tool === 'registry') {
      this.registryCreating.value = view === createCollectionView
      this.registrySelection.value = this.registryCreating.value ? '' : collection
    }
    this.openTool(tool, false)
  }

  private openTool(tool: AdminTool, moveFocus = true) {
    this.activeTool.value = tool
    this.expandedTool.value = hasSubpages(tool) ? tool : undefined
    this.clearFeedback()
    if (tool === 'content') {
      this.contentCollections.value = []
      this.contentOffset.value = null
      void this.loadCollectionNavigation(tool)
    }
    if (tool === 'registry') {
      this.registry.value = undefined
      void this.loadCollectionNavigation(tool)
    }
    this.rememberWorkspace()
    if (moveFocus) void this.focusHeading()
  }

  openPortalPage = (page: PortalSettingsPage, moveFocus = true) => {
    this.portalPage.value = page
    this.openTool(portalSettingsPages[page].section, moveFocus)
  }

  private activateTool(tool: AdminTool) {
    if (isPortalTool(tool)) this.openPortalPage(tool === 'settings' ? 'identity' : 'languages', false)
    else this.openTool(tool, !hasSubpages(tool))
  }

  selectTool = (tool: AdminTool) => {
    if (this.locked()) return
    if (tool === this.activeTool.value) {
      if (hasSubpages(tool)) this.expandedTool.value = this.expandedTool.value === tool ? undefined : tool
      return
    }
    if (isPortalTool(tool) && isPortalTool(this.activeTool.value)) {
      this.activateTool(tool)
      return
    }
    this.navigate(() => {
      this.editingState.value = { dirty: false, busy: false }
      this.activateTool(tool)
    })
  }

  selectPortalPage = (page: PortalSettingsPage) => {
    if (this.locked()) return
    if (page === 'translations') this.permissionTexts.value = false
    this.openPortalPage(page)
  }

  updatePermissionTexts = (enabled: boolean) => {
    this.permissionTexts.value = enabled
    this.rememberWorkspace()
  }

  openPermissionTexts = () => {
    this.navigate(() => {
      this.editingState.value = { dirty: false, busy: false }
      this.permissionTexts.value = true
      this.openPortalPage('translations')
    })
  }

  selectAccessSection = (section: AccessPage) => {
    if (section === this.accessSection.value) return
    this.navigate(() => {
      this.editingState.value = { dirty: false, busy: false }
      this.accessSection.value = section
      this.rememberWorkspace()
      void this.focusHeading()
    })
  }

  private currentCollectionRequest(tool: 'content' | 'registry', request: number) {
    return this.isActive() && request === this.collectionNavigationRequest && this.activeTool.value === tool
  }

  private async updateContentMenu(offset: number, request: number, seek = !offset && Boolean(this.contentSelection.value)): Promise<void> {
    const page = await listCollections(offset)
    if (!this.currentCollectionRequest('content', request)) return
    this.contentCollections.value = offset ? [...this.contentCollections.value, ...page.items] : page.items
    this.contentOffset.value = page.next_offset
    if (seek && !this.selectedContent.value && page.next_offset !== null && page.next_offset > offset) {
      return this.updateContentMenu(page.next_offset, request, true)
    }
    if (!this.selectedContent.value) this.contentSelection.value = this.contentCollections.value[0]?.type_key ?? ''
    this.rememberWorkspace()
  }

  private async updateRegistryMenu(request: number) {
    const result = await listRegistryRenderers()
    if (!this.currentCollectionRequest('registry', request)) return
    this.registry.value = result
    if (!result.collections.some(entry => entry.type_key === this.registrySelection.value)) this.registrySelection.value = ''
    this.rememberWorkspace()
  }

  loadCollectionNavigation = async (tool: 'content' | 'registry', offset = 0) => {
    const request = ++this.collectionNavigationRequest
    this.collectionNavigationLoading.value = true
    this.collectionNavigationError.value = ''
    this.collectionPageError.value = ''
    try {
      if (tool === 'content') await this.updateContentMenu(offset, request)
      else await this.updateRegistryMenu(request)
    }
    catch (error: unknown) {
      if (!this.currentCollectionRequest(tool, request)) return
      if (error instanceof AdminContentError && error.status === 401) {
        await this.sessionChanged()
        return
      }
      const message = error instanceof AdminContentError && error.status === 403 ? 'admin.access_denied' : 'admin.unavailable'
      if (offset > 0) this.collectionPageError.value = message
      else this.collectionNavigationError.value = message
    }
    finally {
      if (request === this.collectionNavigationRequest) this.collectionNavigationLoading.value = false
    }
  }

  selectContent = (key: string) => {
    if (key === this.contentSelection.value) return
    this.navigate(() => {
      this.editingState.value = { dirty: false, busy: false }
      this.contentSelection.value = key
      this.rememberWorkspace()
      void this.focusHeading()
    })
  }

  selectRegistry = (key = '', create = false) => {
    if (key === this.registrySelection.value && create === this.registryCreating.value) return
    this.navigate(() => {
      this.editingState.value = { dirty: false, busy: false }
      this.registrySelection.value = key
      this.registryCreating.value = create
      this.rememberWorkspace()
      ++this.registryForm.value
      void this.focusHeading()
    })
  }

  collectionSaved = (entry: RegistryCollection) => {
    if (!this.registry.value) return
    this.registry.value = {
      items: this.registry.value.items.map(item => item.type_key === entry.type_key ? { ...item, available: false } : item),
      collections: [...this.registry.value.collections.filter(item => item.type_key !== entry.type_key), entry]
        .sort((first, second) => first.type_key.localeCompare(second.type_key)),
    }
    this.registryCreating.value = false
    this.registrySelection.value = entry.enabled ? entry.type_key : ''
    this.rememberWorkspace()
    if (!entry.enabled) {
      this.editingState.value = { dirty: false, busy: false }
      this.notifyRegistryDisabled()
      void this.focusHeading()
    }
  }
}
