<script setup lang="ts">
import { useAsyncData, useHeadSafe, useI18n, useResponseHeader } from '#imports'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { AdminAuthError, restoreAdminSession, submitAdminAuth } from '~/utils/admin-auth'
import type { AdminAuthAction, AdminSessionKind, RestrictedEntryMode } from '~/utils/admin-auth'
import { portalSettingsPages, type PortalSettingsPage } from '~/utils/admin-site-settings'
import { usePublicApi } from '~/composables/usePublicApi'
import { themeModes, type ThemeMode } from '~/composables/usePublicTheme'
import { usePortalAcceptLanguage, usePortalLanguagePreference } from '~/composables/usePortalLanguagePreference'
import { preferredLanguage } from '~/utils/language'
import { publicError } from '~/utils/public-content'
import { compileUiMessage } from '~/utils/ui-message'

defineOptions({ name: 'AdminSignInPage' })

const savedLanguage = usePortalLanguagePreference()
const acceptedLanguage = usePortalAcceptLanguage()
const { client, request } = usePublicApi()

async function loadCatalog(language?: string | null) {
  let result = await request(() => client.getSite(language ? { query: { lang: language } } : undefined))
  if (result.response.status === 400 && language) result = await request(() => client.getSite())
  if (!result.data?.messages['admin.sign_in']) throw publicError(503)
  return result.data
}

const { data: initialCatalog, error } = await useAsyncData('admin-ui-catalog', async () => {
  const saved = savedLanguage.value
  const catalog = await loadCatalog(saved)
  if (saved && catalog.language.code === saved) return catalog
  const chosen = preferredLanguage(catalog.languages.map(item => item.code), catalog.language.code, saved, acceptedLanguage.value)
  return chosen === catalog.language.code ? catalog : loadCatalog(chosen)
})
if (error.value || !initialCatalog.value) throw publicError(503)
const catalog = ref(initialCatalog.value)
// vue-i18n expands dotted keys in place. Keep the API catalog flat for settings search.
const { t, locale, setLocaleMessage } = useI18n({
  useScope: 'local', inheritLocale: false, locale: catalog.value.language.code, fallbackLocale: false,
  flatJson: true, messageCompiler: compileUiMessage,
  messages: { [catalog.value.language.code]: { ...catalog.value.messages } },
})

let catalogRequest = 0
let languageChoice: string | undefined

async function updateCatalog(language: string, remember: boolean) {
  const requestID = ++catalogRequest
  catalogFailure.value = false
  try {
    const next = await loadCatalog(language)
    if (!active || requestID !== catalogRequest) return
    setLocaleMessage(next.language.code, { ...next.messages })
    locale.value = next.language.code
    catalog.value = next
    if (remember && next.language.code === language) savedLanguage.value = language
  }
  catch {
    if (active && requestID === catalogRequest) catalogFailure.value = true
  }
  finally {
    if (requestID === catalogRequest) languageChoice = undefined
  }
}

async function selectLanguage(language: string) {
  if (changingLanguage.value || !catalog.value.languages.some(choice => choice.code === language)) return
  changingLanguage.value = true
  languageChoice = language
  try { await updateCatalog(language, true) }
  finally { changingLanguage.value = false }
}

const theme = ref<ThemeMode>('system')
const changingLanguage = ref(false)
const catalogFailure = ref(false)
const phase = ref<AdminSessionKind | null>(null)
const pending = ref(true)
const notice = ref('')
const restrictedEntry = ref<RestrictedEntryMode | null>(null)
const accessSection = ref<'users' | 'roles' | 'audit' | 'security'>('users')
const confirmingIdentity = ref(false)
const identityDialog = ref<HTMLDialogElement>()
const identityFailure = ref('')
const identityPending = ref(false)
const identityLimited = ref(false)
const failure = ref('')
const limited = ref(false)
const heading = ref<HTMLElement | null>(null)
const csrf = ref('')
let retryTimer: ReturnType<typeof setTimeout> | undefined
let identityRetryTimer: ReturnType<typeof setTimeout> | undefined
let active = true

const domains = ['content', 'registry', 'settings', 'localization', 'access'] as const
type AdminTool = typeof domains[number] | 'account' | 'signed_in'
const activeTool = ref<AdminTool>('signed_in')
const expandedTool = ref<AdminTool>()
const portalPage = ref<PortalSettingsPage>('identity')
const accessPages = ['users', 'roles', 'audit', 'security'] as const
const portalPages = Object.keys(portalSettingsPages) as PortalSettingsPage[]
const workspaceOpen = computed(() => phase.value === 'full')
const editingState = ref({ dirty: false, busy: false })
const leaveDialog = ref<HTMLDialogElement>()
let leaveAction: (() => void) | undefined
let workspaceFocus: HTMLElement | undefined
let identityReturnFocus: HTMLElement | undefined

function warnBeforeLeaving(event: BeforeUnloadEvent) {
  event.preventDefault()
}

watch(() => editingState.value.dirty, dirty => {
  if (dirty) window.addEventListener('beforeunload', warnBeforeLeaving)
  else window.removeEventListener('beforeunload', warnBeforeLeaving)
})

watch(phase, kind => {
  if (kind !== 'full' && confirmingIdentity.value) void closeIdentityDialog()
})

const title = computed(() => {
  if (restrictedEntry.value) return restrictedEntry.value === 'enrollment' ? 'entry_enrollment' : 'entry_recovery'
  switch (phase.value) {
    case 'preauth': return 'sign_in'
    case 'login': return 'two_step'
    case 'full': return workspaceTitle.value
    case null: return pending.value ? 'checking' : 'name'
    default: return 'name'
  }
})
const workspaceTitle = computed(() => {
  if (isPortalTool(activeTool.value)) return portalSettingsPages[portalPage.value].label
  return activeTool.value === 'registry' ? 'workspace_structure' : activeTool.value
})
const blocked = computed(() => pending.value || limited.value)
const navigationLocked = computed(() => blocked.value || editingState.value.busy || confirmingIdentity.value)
const localizedTitle = computed(() => t(`admin.${title.value}`))
useHeadSafe(() => ({
  title: `${localizedTitle.value} · ${catalog.value.settings.title}`,
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
  phase.value = null
  failure.value = error instanceof AdminAuthError && (error.status === 401 || error.status === 403)
    ? 'changed' : 'unavailable'
}

async function refresh(moveFocus = false) {
  pending.value = true
  failure.value = ''
  try {
    const restored = await restoreAdminSession()
    if (!active) return
    csrf.value = restored.csrf
    phase.value = restored.kind
    if (restored.kind === 'enrollment' || restored.kind === 'recovery') restrictedEntry.value = restored.kind
    if (restored.kind !== 'full') {
      activeTool.value = 'signed_in'
      expandedTool.value = undefined
      editingState.value = { dirty: false, busy: false }
    }
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

async function submit(action: Exclude<AdminAuthAction, 'step-up'>, event?: SubmitEvent) {
  if (blocked.value) return
  const form = event?.currentTarget
  if (action !== 'logout' && !(form instanceof HTMLFormElement)) return
  pending.value = true
  notice.value = ''
  failure.value = ''
  try {
    let fields = {}
    if (form instanceof HTMLFormElement) {
      if (action === 'login') fields = takeAuthFields(form, 'login', 'password')
      if (action === 'mfa/verify') fields = takeAuthFields(form, 'code')
    }
    csrf.value = await submitAdminAuth(action, csrf.value, fields)
    if (!active) return
    await afterAuthAction(action)
    await focusHeading()
  }
  catch (error: unknown) {
    if (active) handleFailure(error, action)
  }
  finally {
    pending.value = false
  }
}

async function afterAuthAction(action: Exclude<AdminAuthAction, 'step-up'>) {
  if (action === 'logout') {
    notice.value = 'signed_out'
    phase.value = null
    activeTool.value = 'signed_in'
    expandedTool.value = undefined
    editingState.value = { dirty: false, busy: false }
    await refresh()
  }
  else {
    phase.value = action === 'login' ? 'login' : 'full'
  }
}

function navigate(action: () => void) {
  if (editingState.value.busy || blocked.value || confirmingIdentity.value) return
  if (!editingState.value.dirty) {
    action()
    return
  }
  leaveAction = action
  leaveDialog.value?.showModal()
}

function completeNavigation() {
  const action = leaveAction
  leaveAction = undefined
  leaveDialog.value?.close()
  action?.()
}

function isPortalTool(tool: AdminTool): tool is 'settings' | 'localization' {
  return tool === 'settings' || tool === 'localization'
}

function hasSubpages(tool: AdminTool) {
  return isPortalTool(tool) || tool === 'access'
}

function openTool(tool: AdminTool, moveFocus = true) {
  activeTool.value = tool
  expandedTool.value = hasSubpages(tool) ? tool : undefined
  failure.value = ''
  notice.value = ''
  if (moveFocus) void focusHeading()
}

function openPortalPage(page: PortalSettingsPage, moveFocus = true) {
  portalPage.value = page
  openTool(portalSettingsPages[page].section, moveFocus)
}

function activateTool(tool: AdminTool) {
  if (isPortalTool(tool)) openPortalPage(tool === 'settings' ? 'identity' : 'languages', false)
  else openTool(tool, !hasSubpages(tool))
}

function selectTool(tool: AdminTool) {
  if (navigationLocked.value) return
  if (tool === activeTool.value) {
    if (hasSubpages(tool)) expandedTool.value = expandedTool.value === tool ? undefined : tool
    return
  }
  if (isPortalTool(tool) && isPortalTool(activeTool.value)) {
    activateTool(tool)
    return
  }
  navigate(() => {
    editingState.value = { dirty: false, busy: false }
    activateTool(tool)
  })
}

function selectPortalPage(page: PortalSettingsPage) {
  if (!navigationLocked.value) openPortalPage(page)
}

function selectAccessSection(section: typeof accessSection.value) {
  if (section === accessSection.value) return
  navigate(() => {
    editingState.value = { dirty: false, busy: false }
    accessSection.value = section
    void focusHeading()
  })
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
  await updateCatalog(languageChoice ?? locale.value, languageChoice !== undefined)
}

onMounted(() => {
  loadPreferences()
  void refresh()
})
onBeforeUnmount(() => {
  active = false
  csrf.value = ''
  window.removeEventListener('beforeunload', warnBeforeLeaving)
  if (retryTimer) clearTimeout(retryTimer)
  if (identityRetryTimer) clearTimeout(identityRetryTimer)
})
</script>

<template>
  <main class="admin-shell">
    <header class="admin-header" :class="{ 'admin-header--signed-in': workspaceOpen }">
      <div class="admin-brand">
        <span class="admin-name">{{ catalog.settings.title }}</span>
        <small>{{ t('admin.name') }}</small>
      </div>
      <div class="admin-preferences">
        <LanguageSwitcher
          :label="t('admin.language')" :current="catalog.language" :choices="catalog.languages"
          :busy="changingLanguage" @select="selectLanguage"
        />
        <ThemeSwitcher v-model="theme" :translate="t" @update:model-value="savePreference('admin-theme', $event)" />
        <template v-if="workspaceOpen">
          <button
            type="button" class="admin-header-action"
            :aria-label="t('admin.account')" :title="t('admin.account')"
            :aria-current="activeTool === 'account' ? 'page' : undefined" :disabled="blocked || editingState.busy"
            @click="selectTool('account')"
          ><PreferenceIcon name="account" /></button>
          <button
            type="button" class="admin-header-action"
            :aria-label="t('admin.sign_out')" :title="t('admin.sign_out')" :disabled="blocked || editingState.busy"
            @click="navigate(() => { void submit('logout') })"
          ><PreferenceIcon name="sign-out" /></button>
        </template>
      </div>
    </header>

    <dialog ref="leaveDialog" class="admin-dialog admin-leave" aria-labelledby="admin-leave-title" @close="leaveAction = undefined">
      <h2 id="admin-leave-title">{{ t('admin.leave_unsaved') }}</h2>
      <form method="dialog">
        <button type="submit" autofocus>{{ t('admin.stay_editing') }}</button>
        <button type="button" @click="completeNavigation">{{ t('admin.discard_changes') }}</button>
      </form>
    </dialog>
    <dialog
      ref="identityDialog" class="admin-dialog admin-step-up" aria-labelledby="admin-step-up-title"
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
      <nav v-if="workspaceOpen" class="admin-navigation" :aria-label="t('admin.name')">
        <ul class="admin-nav-domains">
          <li v-for="tool in domains" :key="tool" :class="{ 'admin-nav-open': expandedTool === tool }">
            <button
              type="button" class="admin-nav-parent" :class="{ 'admin-nav-active': activeTool === tool }"
              :aria-current="activeTool === tool && !hasSubpages(tool) ? 'page' : undefined"
              :aria-expanded="hasSubpages(tool) ? expandedTool === tool : undefined"
              :aria-controls="hasSubpages(tool) ? `admin-nav-${tool}` : undefined"
              :aria-disabled="navigationLocked || undefined" @click="selectTool(tool)"
            >
              {{ t(tool === 'registry' ? 'admin.workspace_structure' : `admin.${tool}`) }}
              <span v-if="hasSubpages(tool)" class="admin-nav-chevron" aria-hidden="true" />
            </button>
            <ul v-if="hasSubpages(tool)" v-show="expandedTool === tool" :id="`admin-nav-${tool}`" class="admin-nav-children">
              <template v-if="isPortalTool(tool)">
                <li v-for="page in portalPages.filter(item => portalSettingsPages[item].section === tool)" :key="page">
                  <button
                    type="button" :aria-current="activeTool === tool && portalPage === page ? 'page' : undefined"
                    :aria-disabled="navigationLocked || undefined" @click="selectPortalPage(page)"
                  >{{ t(`admin.${portalSettingsPages[page].label}`) }}</button>
                </li>
              </template>
              <template v-else>
                <li v-for="page in accessPages" :key="page">
                  <button
                    type="button" :aria-current="activeTool === tool && accessSection === page ? 'page' : undefined"
                    :aria-disabled="navigationLocked || undefined" @click="selectAccessSection(page)"
                  >{{ t(`admin.${page}`) }}</button>
                </li>
              </template>
            </ul>
          </li>
        </ul>
      </nav>
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
          <input id="admin-login" name="login" type="text" required autocomplete="username" spellcheck="false">
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

        <template v-else-if="phase === 'full'">
          <PortalSettings
            v-if="isPortalTool(activeTool)"
            :page="portalPage" :csrf="csrf" :ui-language="locale" :ui-messages="catalog.messages" :translate="t"
            @show-page="openPortalPage" @published="refreshPublishedCatalog"
            @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
          />
          <AdminContentWorkspace
            v-if="activeTool === 'content'" :csrf="csrf" :ui-language="locale" :translate="t"
            :default-language="catalog.settings.default_language" :slug-rules="catalog.slug_rules ?? []"
            @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
          />
          <AdminTypeBuilder
            v-if="activeTool === 'registry'" :csrf="csrf" :ui-language="locale" :translate="t"
            :languages="catalog.languages"
            @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
          />
          <AdminAccount
            v-if="activeTool === 'account'" :csrf="csrf" :ui-language="locale" :translate="t"
            @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event"
          />
          <template v-if="activeTool === 'access'">
            <AdminUsers v-if="accessSection === 'users'" :csrf="csrf" :ui-language="locale" :translate="t" @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event" />
            <AdminRoles v-else-if="accessSection === 'roles'" :csrf="csrf" :translate="t" @step-up="confirmActionIdentity" @session-changed="refresh(true)" @editing-state="editingState = $event" />
            <AdminAudit v-else-if="accessSection === 'audit'" :ui-language="locale" :translate="t" @session-changed="refresh(true)" />
            <AdminSessionPolicy
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
</style>
