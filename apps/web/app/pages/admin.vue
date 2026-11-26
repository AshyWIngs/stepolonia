<script setup lang="ts">
import { useAsyncData, useHeadSafe, useI18n, useResponseHeader, useRoute, useRouter } from '#imports'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { AdminAuthError, isAdminPreauthConflict, restoreAdminSession, submitAdminAuth } from '~/utils/admin-auth'
import type { AdminAuthAction, AdminSessionKind, RestrictedEntryMode } from '~/utils/admin-auth'
import { portalSettingsPages } from '~/utils/admin-site-settings'
import { useAdminUI, type AdminEntryCatalog, type AdminWorkspaceCatalog } from '~/composables/useAdminUI'
import { themeModes, type ThemeMode } from '~/composables/usePublicTheme'
import { usePortalAcceptLanguage, usePortalLanguagePreference } from '~/composables/usePortalLanguagePreference'
import { preferredLanguage } from '~/utils/language'
import { publicError } from '~/utils/public-content'
import { AdminCatalogs } from '~/features/admin-workspace/AdminCatalogs'
import { AdminWorkspaceNavigation, isPortalTool } from '~/features/admin-workspace/AdminWorkspaceNavigation'
import AdminWorkspaceSidebar from '~/features/admin-workspace/AdminWorkspaceSidebar.vue'
import { compileUiMessage } from '~/utils/ui-message'

defineOptions({ name: 'AdminSignInPage' })

const savedLanguage = usePortalLanguagePreference()
const acceptedLanguage = usePortalAcceptLanguage()
const adminUI = useAdminUI()
const route = useRoute()
const router = useRouter()

const { data: initialCatalog, error } = await useAsyncData('admin-ui-entry', async () => {
  const saved = savedLanguage.value
  const catalog = await adminUI.readEntry(saved)
  if (saved && catalog.language.code === saved) return catalog
  const chosen = preferredLanguage(catalog.languages.map(item => item.code), catalog.language.code, saved, acceptedLanguage.value)
  return chosen === catalog.language.code ? catalog : adminUI.readEntry(chosen)
})
if (error.value || !initialCatalog.value) throw publicError(503)
// vue-i18n expands dotted keys in place. Keep the API catalog flat for settings search.
const { t, locale, setLocaleMessage } = useI18n({
  useScope: 'local', inheritLocale: false, locale: initialCatalog.value.language.code, fallbackLocale: false,
  flatJson: true, messageCompiler: compileUiMessage,
  messages: { [initialCatalog.value.language.code]: { ...initialCatalog.value.messages } },
})

function applyCatalogLocale(next: AdminEntryCatalog) {
  if (locale.value !== next.language.code) setLocaleMessage(locale.value, {})
  setLocaleMessage(next.language.code, { ...next.messages })
  locale.value = next.language.code
}

function resetWorkspace() {
  catalogs.clearWorkspace()
  navigation.resetWorkspace()
}

async function refreshChangedAccount(language: string) {
  resetWorkspace()
  csrf.value = ''
  phase.value = null
  await refresh(true, language)
}

function enterWorkspace(language = locale.value) {
  return catalogs.openWorkspace(language, activateWorkspace)
}

async function activateWorkspace(next: AdminWorkspaceCatalog) {
  if (catalogs.changedWorkspaceAccount(next)) {
    resetWorkspace()
    phase.value = null
    await nextTick()
    if (!active) return
  }
  catalogs.activateWorkspace(next)
  restrictedEntry.value = null
  phase.value = 'full'
}

const theme = ref<ThemeMode>('system')
const phase = ref<AdminSessionKind | null>(null)
const pending = ref(true)
const notice = ref('')
const restrictedEntry = ref<RestrictedEntryMode | null>(null)
const confirmingIdentity = ref(false)
const identityDialog = ref<HTMLDialogElement>()
const identityFailure = ref('')
const identityPending = ref(false)
const identityLimited = ref(false)
const failure = ref('')
const limited = ref(false)
const heading = ref<HTMLElement | null>(null)
const csrf = ref('')
const signInLogin = ref('')
let retryTimer: ReturnType<typeof setTimeout> | undefined
let identityRetryTimer: ReturnType<typeof setTimeout> | undefined
let active = true

const navigation: AdminWorkspaceNavigation = new AdminWorkspaceNavigation(
  route, router, () => active, () => navigationLocked.value, focusHeading,
  () => {
    failure.value = ''
    notice.value = ''
  },
  () => { notice.value = 'registry_disabled_notice' },
  () => refresh(true),
)
const {
  activeTool, editingState, leaveDialog, accessSection, portalPage, permissionTexts,
  collectionNavigationLoading, collectionNavigationError, selectedContent, registry, registrySelection, registryCreating, registryForm,
  disabledCollections,
} = navigation
const showNavigationLabels = ref(false)
const workspaceOpen = computed(() => phase.value === 'full')
const catalogs: AdminCatalogs = new AdminCatalogs(initialCatalog.value, adminUI, {
  isActive: () => active,
  workspaceOpen: () => workspaceOpen.value,
  sessionPending: () => pending.value,
  changeLocale: applyCatalogLocale,
  rememberLanguage: language => { savedLanguage.value = language },
  refreshChangedAccount,
  sessionChanged: () => refresh(true),
})
const { workspaceCatalog, catalog, changingLanguage, catalogFailure } = catalogs
let workspaceFocus: HTMLElement | undefined
let identityReturnFocus: HTMLElement | undefined

function warnBeforeLeaving(event: BeforeUnloadEvent) {
  event.preventDefault()
}

watch(() => editingState.value.dirty, dirty => {
  if (dirty) window.addEventListener('beforeunload', warnBeforeLeaving)
  else window.removeEventListener('beforeunload', warnBeforeLeaving)
})

watch(phase, (kind, previous) => {
  if (kind !== 'full') {
    if (confirmingIdentity.value) void closeIdentityDialog()
    navigation.resetSession()
  }
  else if (previous !== 'full') navigation.restoreWorkspace()
})

const titleKey = computed(() => {
  if (restrictedEntry.value) return restrictedEntry.value === 'enrollment' ? 'admin.entry_enrollment' : 'admin.entry_recovery'
  switch (phase.value) {
    case 'preauth': return 'admin.sign_in'
    case 'login': return 'admin.two_step'
    case 'full': return workspaceTitleKey.value
    case null: return pending.value ? 'admin.checking' : 'admin.name'
    default: return 'admin.name'
  }
})
const workspaceTitleKey = computed(() => {
  if (isPortalTool(activeTool.value)) return portalSettingsPages[portalPage.value].label
  if (activeTool.value === 'registry') return registryCreating.value ? 'admin.registry_create' : 'admin.workspace_structure'
  return `admin.${activeTool.value}`
})
const blocked = computed(() => pending.value || limited.value)
const navigationLocked = computed(() => blocked.value || editingState.value.busy || confirmingIdentity.value)
const localizedTitle = computed(() => {
  if (phase.value === 'full' && activeTool.value === 'registry' && !registryCreating.value && registrySelection.value) {
    const entry = registry.value?.collections.find(entry => entry.type_key === registrySelection.value)
    if (entry) return collectionLabel(entry)
  }
  return t(titleKey.value)
})
useHeadSafe(() => ({
  title: `${localizedTitle.value} · ${catalog.value.title}`,
  htmlAttrs: { lang: locale.value, dir: catalog.value.language.direction, 'data-admin-theme': theme.value },
}))
useResponseHeader('Cache-Control').value = 'no-store'

function savePreference(key: string, value: string) {
  try { localStorage.setItem(key, value) }
  catch { /* The preference still applies for this visit. */ }
}

function loadPreferences() {
  try {
    const savedTheme = localStorage.getItem('admin-theme')
    theme.value = themeModes.find(value => value === savedTheme) ?? 'system'
  }
  catch { /* Preferences are optional when browser storage is unavailable. */ }
}

async function focusHeading() {
  await nextTick()
  heading.value?.focus()
}

function handleFailure(error: unknown, action?: AdminAuthAction) {
  if (error instanceof AdminAuthError && error.status === 429) {
    failure.value = 'limited'
    limited.value = true
    retryTimer = setTimeout(() => { limited.value = false }, Math.min(error.retryAfter ?? 900, 86400) * 1000)
    return
  }
  if (error instanceof AdminAuthError && error.code === 'invalid_credentials') {
    failure.value = action === 'mfa/verify' ? 'invalid_code' : 'invalid_credentials'
    return
  }
  // A failed response may follow a successful cookie or token rotation.
  csrf.value = ''
  resetWorkspace()
  phase.value = null
  failure.value = error instanceof AdminAuthError && (error.status === 401 || error.status === 403)
    ? 'changed' : 'unavailable'
}

async function applyRestoredSession(restored: Awaited<ReturnType<typeof restoreAdminSession>>, requestedLanguage?: string) {
  csrf.value = restored.csrf
  if (restored.kind === 'full') {
    await enterWorkspace(requestedLanguage)
    return
  }
  const language = requestedLanguage ?? (workspaceCatalog.value ? locale.value : undefined)
  resetWorkspace()
  phase.value = restored.kind
  if (restored.kind === 'enrollment' || restored.kind === 'recovery') restrictedEntry.value = restored.kind
  if (language) await catalogs.update(language, false)
}

async function refresh(moveFocus = false, requestedLanguage?: string) {
  pending.value = true
  failure.value = ''
  try {
    const restored = await restoreAdminSession()
    if (!active) return
    await applyRestoredSession(restored, requestedLanguage)
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    pending.value = false
  }
  if (moveFocus) await focusHeading()
}

function takeAuthFields(form: HTMLFormElement, ...names: Array<'login' | 'password' | 'code'>): Record<string, string> {
  const data = new FormData(form)
  return Object.fromEntries(names.map((name) => {
    const field = form.elements.namedItem(name)
    if (name !== 'login' && field instanceof HTMLInputElement) field.value = ''
    const value = data.get(name)
    return [name, typeof value === 'string' ? value : '']
  }))
}

async function prepareSignIn(login: string) {
  signInLogin.value = login
  const restored = await restoreAdminSession()
  if (!active) return false
  await applyRestoredSession(restored)
  if (restored.kind === 'preauth') return true
  await focusHeading()
  return false
}

async function showAuthFailure(error: unknown, action: AdminAuthAction) {
  if (!active) return
  if (action === 'login' && isAdminPreauthConflict(error)) {
    await refresh(true)
    if (phase.value === 'preauth') failure.value = 'sign_in_retry'
    return
  }
  handleFailure(error, action)
}

async function submit(action: Exclude<AdminAuthAction, 'step-up'>, event?: SubmitEvent) {
  if (blocked.value) return
  const form = event?.currentTarget
  if (action !== 'logout' && !(form instanceof HTMLFormElement)) return
  pending.value = true
  notice.value = ''
  failure.value = ''
  try {
    let fields: Record<string, string> = {}
    if (form instanceof HTMLFormElement) {
      if (action === 'login') {
        fields = takeAuthFields(form, 'login', 'password')
        if (!await prepareSignIn(fields.login ?? '')) return
      }
      if (action === 'mfa/verify') fields = takeAuthFields(form, 'code')
    }
    csrf.value = await submitAdminAuth(action, csrf.value, fields)
    if (!active) return
    await afterAuthAction(action)
    await focusHeading()
  }
  catch (error: unknown) {
    await showAuthFailure(error, action)
  }
  finally {
    pending.value = false
  }
}

async function afterAuthAction(action: Exclude<AdminAuthAction, 'step-up'>) {
  if (action === 'logout') {
    const language = locale.value
    notice.value = 'signed_out'
    resetWorkspace()
    phase.value = null
    await refresh(false, language)
  }
  else if (action === 'login') phase.value = 'login'
  else await enterWorkspace()
}

function collectionLabel(entry: { type_key: string, display_name?: string }) {
  const key = `admin.registry_${entry.type_key}`
  return entry.display_name || (Object.hasOwn(catalog.value.messages, key) ? t(key) : t('collection'))
}

async function changeRestrictedEntry(mode: RestrictedEntryMode | null) {
  if (restrictedEntry.value && phase.value === restrictedEntry.value) {
    const oldPhase = phase.value
    await submit('logout')
    if (phase.value === oldPhase || phase.value === null) return
  }
  restrictedEntry.value = mode
  failure.value = ''
  if (!phase.value) await refresh(true)
  else await focusHeading()
}

function receiveRestrictedCSRF(token: string) {
  csrf.value = token
  phase.value = restrictedEntry.value
}

function completeRestrictedEntry() {
  csrf.value = ''
  phase.value = null
}

function rememberWorkspaceFocus(event: FocusEvent | MouseEvent | KeyboardEvent) {
  if (!(event.target instanceof Element)) return
  const control = event.target.closest<HTMLElement>('button, input, select, textarea, a, summary, [tabindex]')
  if (!control) return
  workspaceFocus = control
  if (event.type === 'click' && notice.value === 'identity_confirmed') notice.value = ''
}

async function confirmActionIdentity() {
  if (confirmingIdentity.value || phase.value !== 'full') return
  identityReturnFocus = workspaceFocus
  confirmingIdentity.value = true
  identityFailure.value = identityLimited.value ? 'limited' : ''
  notice.value = ''
  await nextTick()
  if (active && confirmingIdentity.value) identityDialog.value?.showModal()
}

async function closeIdentityDialog() {
  const target = identityReturnFocus
  identityReturnFocus = undefined
  confirmingIdentity.value = false
  identityFailure.value = ''
  identityDialog.value?.close()
  await nextTick()
  if (phase.value === 'full') {
    target?.focus()
    if (document.activeElement !== target) heading.value?.focus()
  }
}

function cancelIdentityConfirmation() {
  if (!identityPending.value) void closeIdentityDialog()
}

async function handleIdentityFailure(error: unknown) {
  identityFailure.value = 'unavailable'
  if (!(error instanceof AdminAuthError)) return
  if (error.code === 'invalid_credentials') {
    identityFailure.value = 'invalid_credentials'
  }
  else if (error.status === 429) {
    identityFailure.value = 'limited'
    identityLimited.value = true
    identityRetryTimer = setTimeout(() => { identityLimited.value = false }, Math.min(error.retryAfter ?? 900, 86400) * 1000)
  }
  else if (error.status === 401 || error.code === 'csrf_failed') {
    identityFailure.value = 'changed'
    await refresh(true)
  }
}

async function submitIdentityConfirmation(event: SubmitEvent) {
  if (identityPending.value || identityLimited.value) return
  const form = event.currentTarget
  if (!(form instanceof HTMLFormElement)) return
  identityPending.value = true
  identityFailure.value = ''
  const fields = takeAuthFields(form, 'password', 'code')
  try {
    const token = await submitAdminAuth('step-up', csrf.value, fields)
    if (!active) return
    csrf.value = token
    notice.value = 'identity_confirmed'
    await closeIdentityDialog()
  }
  catch (error: unknown) {
    if (active) await handleIdentityFailure(error)
  }
  finally {
    identityPending.value = false
  }
}

async function refreshPublishedCatalog() {
  await catalogs.refreshPublished(locale.value)
}

function revalidateWorkspaceOnFocus() {
  if (workspaceOpen.value && !pending.value && !changingLanguage.value) void catalogs.update(locale.value, false)
}

onMounted(() => {
  loadPreferences()
  window.addEventListener('focus', revalidateWorkspaceOnFocus)
  void refresh()
})
onBeforeUnmount(() => {
  active = false
  csrf.value = ''
  window.removeEventListener('focus', revalidateWorkspaceOnFocus)
  window.removeEventListener('beforeunload', warnBeforeLeaving)
  if (retryTimer) clearTimeout(retryTimer)
  if (identityRetryTimer) clearTimeout(identityRetryTimer)
})
</script>

<template>
  <main class="admin-shell">
    <header class="admin-header" :class="{ 'admin-header--signed-in': workspaceOpen }">
      <div class="admin-brand">
        <span class="admin-name">{{ catalog.title }}</span>
        <small>{{ t('admin.name') }}</small>
      </div>
      <div class="admin-preferences">
        <LanguageSwitcher
          :label="t('common.language')" :current="catalog.language" :choices="catalog.languages"
          :flags="catalog.language_flags"
          :busy="pending || changingLanguage" @select="catalogs.selectLanguage"
        />
        <ThemeSwitcher v-model="theme" :translate="t" @update:model-value="savePreference('admin-theme', $event)" />
        <template v-if="workspaceOpen">
          <button
            type="button" class="admin-header-action"
            :aria-label="t('admin.account')" :title="t('admin.account')"
            :aria-current="activeTool === 'account' ? 'page' : undefined" :disabled="blocked || editingState.busy"
            @click="navigation.selectTool('account')"
          ><PreferenceIcon name="account" /></button>
          <button
            type="button" class="admin-header-action"
            :aria-label="t('admin.sign_out')" :title="t('admin.sign_out')" :disabled="blocked || editingState.busy"
            @click="navigation.navigate(() => { void submit('logout') })"
          ><PreferenceIcon name="sign-out" /></button>
        </template>
      </div>
    </header>

    <dialog v-if="workspaceOpen" ref="leaveDialog" class="admin-dialog admin-leave" aria-labelledby="admin-leave-title" @close="navigation.clearLeaveAction">
      <h2 id="admin-leave-title">{{ t('admin.leave_unsaved') }}</h2>
      <form method="dialog">
        <button type="submit" autofocus>{{ t('admin.stay_editing') }}</button>
        <button type="button" @click="navigation.completeNavigation">{{ t('admin.discard_changes') }}</button>
      </form>
    </dialog>
    <dialog
      v-if="workspaceOpen" ref="identityDialog" class="admin-dialog admin-step-up" aria-labelledby="admin-step-up-title"
      aria-describedby="admin-step-up-help" @cancel.prevent="cancelIdentityConfirmation"
    >
      <h2 id="admin-step-up-title">{{ t('admin.fresh_auth') }}</h2>
      <p id="admin-step-up-help">{{ t('admin.step_up_required') }}</p>
      <p v-if="identityFailure" id="admin-step-up-error" class="admin-error" role="alert">{{ t(`admin.${identityFailure}`) }}</p>
      <output v-if="identityPending" class="admin-status">{{ t('admin.working') }}</output>
      <form v-if="confirmingIdentity" :aria-busy="identityPending" @submit.prevent="submitIdentityConfirmation">
        <label for="admin-step-up-password">{{ t('admin.password') }}</label>
        <input
          id="admin-step-up-password" name="password" type="password" autocomplete="current-password" required autofocus
          :readonly="identityPending" :aria-describedby="identityFailure ? 'admin-step-up-error' : undefined"
        >
        <label for="admin-step-up-code">{{ t('admin.code') }}</label>
        <input
          id="admin-step-up-code" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required
          :readonly="identityPending" :aria-describedby="identityFailure ? 'admin-step-up-error' : undefined"
        >
        <div class="admin-step-up-actions">
          <button type="submit" :disabled="identityPending || identityLimited">{{ t('admin.unlock') }}</button>
          <button type="button" class="admin-secondary" :disabled="identityPending" @click="cancelIdentityConfirmation">{{ t('admin.cancel') }}</button>
        </div>
      </form>
    </dialog>
    <div :class="{ 'admin-workspace': workspaceOpen }">
      <AdminWorkspaceSidebar
        v-if="workspaceOpen" v-model:show-labels="showNavigationLabels"
        :navigation="navigation" :locked="navigationLocked" :translate="t" :collection-label="collectionLabel"
      />
      <section
        class="admin-card" :class="{ 'admin-card--workspace': workspaceOpen }" aria-labelledby="admin-heading"
        @focusin="rememberWorkspaceFocus" @click.capture="rememberWorkspaceFocus" @keydown.capture="rememberWorkspaceFocus"
      >
        <h1 id="admin-heading" ref="heading" tabindex="-1">{{ localizedTitle }}</h1>
        <output v-if="notice" class="admin-status">{{ t(`admin.${notice}`) }}</output>
        <p v-if="failure" id="admin-auth-error" class="admin-error" role="alert">{{ t(`admin.${failure}`) }}</p>
        <p v-if="catalogFailure" class="admin-error" role="alert">{{ t('admin.unavailable') }}</p>
        <output v-if="pending && phase !== null" class="admin-status">{{ t('admin.working') }}</output>

        <AdminRestrictedEntry
          v-if="restrictedEntry" :mode="restrictedEntry" :phase="phase" :csrf="csrf" :translate="t"
          @started="receiveRestrictedCSRF" @finished="completeRestrictedEntry" @restart="changeRestrictedEntry(null)"
        />
        <form v-else-if="phase === 'preauth'" :aria-busy="pending" :aria-describedby="failure ? 'admin-auth-error' : undefined" @submit.prevent="submit('login', $event)">
          <label for="admin-login">{{ t('admin.login') }}</label>
          <input id="admin-login" name="login" type="text" :value="signInLogin" required autocomplete="username" spellcheck="false">
          <label for="admin-password">{{ t('admin.password') }}</label>
          <input id="admin-password" name="password" type="password" required autocomplete="current-password">
          <button type="submit" :disabled="blocked">{{ t('admin.continue') }}</button>
          <button type="button" class="admin-secondary" :disabled="blocked" @click="changeRestrictedEntry('enrollment')">{{ t('admin.entry_enrollment') }}</button>
          <button type="button" class="admin-secondary" :disabled="blocked" @click="changeRestrictedEntry('recovery')">{{ t('admin.entry_recovery') }}</button>
        </form>

        <form v-else-if="phase === 'login'" :aria-busy="pending" @submit.prevent="submit('mfa/verify', $event)">
          <p id="admin-code-help">{{ t('admin.code_help') }}</p>
          <label for="admin-code">{{ t('admin.code') }}</label>
          <input
            id="admin-code" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required
            :aria-describedby="failure ? 'admin-code-help admin-auth-error' : 'admin-code-help'"
          >
          <button type="submit" :disabled="blocked">{{ t('admin.verify') }}</button>
          <button type="button" class="admin-secondary" :disabled="blocked" @click="submit('logout')">{{ t('admin.restart') }}</button>
        </form>

        <template v-else-if="phase === 'full' && workspaceCatalog">
          <PortalSettings
            v-if="isPortalTool(activeTool)"
            :page="portalPage" :permission-texts="permissionTexts" :csrf="csrf" :ui-language="locale" :ui-messages="catalog.messages" :translate="t"
            @update:permission-texts="navigation.updatePermissionTexts"
            @show-page="navigation.openPortalPage" @published="refreshPublishedCatalog"
            @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
          />
          <template v-if="activeTool === 'content' || activeTool === 'registry'">
            <p v-if="collectionNavigationLoading"><output>{{ t('admin.working') }}</output></p>
            <template v-if="collectionNavigationError">
              <p role="alert">{{ t(collectionNavigationError) }}</p>
              <button type="button" @click="navigation.loadCollectionNavigation(activeTool)">{{ t('admin.retry') }}</button>
            </template>
            <AdminContentWorkspace
              v-else-if="activeTool === 'content' && selectedContent" :key="selectedContent.type_id"
              :collection="selectedContent" :collection-label="collectionLabel(selectedContent)" :csrf="csrf" :ui-language="locale" :translate="t"
              :default-language="workspaceCatalog.default_language" :slug-rules="workspaceCatalog.slug_rules"
              @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
            />
            <p v-else-if="activeTool === 'content' && !collectionNavigationLoading">{{ t('admin.content_no_collections') }}</p>
            <template v-else-if="activeTool === 'registry' && registry">
              <AdminTypeBuilder
                v-if="registryCreating || registrySelection" :key="registryForm"
                :collection-key="registrySelection" :renderers="registry.items" :csrf="csrf" :ui-language="locale" :translate="t"
                :languages="catalog.languages" :slug-rules="workspaceCatalog.slug_rules" :collection-label="collectionLabel"
                @saved="navigation.collectionSaved" @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
              />
              <div v-else class="admin-collection-overview">
                <button type="button" class="admin-collection-action" @click="navigation.selectRegistry('', true)">
                  <AdminIcon name="add" />
                  {{ t('admin.registry_create') }}
                </button>
                <details v-if="disabledCollections.length">
                  <summary>{{ t('admin.registry_disabled_collections') }}</summary>
                  <ul>
                    <li v-for="entry in disabledCollections" :key="entry.type_id">
                      <button type="button" class="admin-secondary admin-collection-action" @click="navigation.selectRegistry(entry.type_key)">
                        <AdminIcon name="collection" :icon-svg="entry.icon_svg" />
                        <span dir="auto">{{ collectionLabel(entry) }}</span>
                      </button>
                    </li>
                  </ul>
                </details>
              </div>
            </template>
          </template>
          <AdminAccount
            v-if="activeTool === 'account'" :csrf="csrf" :ui-language="locale" :translate="t"
            @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
          />
          <template v-if="activeTool === 'access'">
            <AdminUsers v-if="accessSection === 'users'" :csrf="csrf" :ui-language="locale" :translate="t" @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event" />
            <AdminRoles v-else-if="accessSection === 'roles'" :csrf="csrf" :ui-language="locale" :translate="t" @permission-texts="navigation.openPermissionTexts" @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event" />
            <AdminAudit v-else-if="accessSection === 'audit'" :ui-language="locale" :translate="t" @session-changed="refresh(true)" />
            <AdminSessionPolicy
              v-else-if="accessSection === 'security'" :csrf="csrf" :translate="t"
              @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
            />
            <AdminRatePolicy
              v-else :csrf="csrf" :translate="t"
              @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
            />
          </template>
        </template>
        <template v-else-if="phase">
          <p>{{ t('admin.restricted') }}</p>
          <button type="button" :disabled="blocked" @click="submit('logout')">{{ t('admin.restart') }}</button>
        </template>
        <button v-else-if="!pending" type="button" :disabled="limited" @click="refresh(true)">{{ t('admin.retry') }}</button>
      </section>
    </div>
  </main>
</template>

<style scoped>
@import '../assets/admin-sign-in.css';

.admin-collection-overview {
  display: grid;
  justify-items: start;
  gap: 1rem;
}

.admin-collection-action {
  display: inline-flex;
  align-items: center;
  gap: .5rem;
}

.admin-collection-overview ul {
  display: grid;
  gap: .5rem;
  list-style: none;
  padding-inline-start: 0;
}

.admin-collection-overview summary {
  align-content: center;
  min-block-size: 44px;
  cursor: pointer;
}
</style>
