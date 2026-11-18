<script setup lang="ts">
import { useAsyncData, useCookie, useHeadSafe, useI18n, useResponseHeader } from '#imports'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { AdminAuthError, restoreAdminSession, submitAdminAuth } from '~/utils/admin-auth'
import type { AdminAuthAction, AdminSessionKind, RestrictedEntryMode } from '~/utils/admin-auth'
import { usePublicApi } from '~/composables/usePublicApi'
import { publicError } from '~/utils/public-content'

defineOptions({ name: 'AdminSignInPage' })

const preferredLanguage = useCookie<string | null>('admin-language', { sameSite: 'lax', secure: true, maxAge: 31536000 })
const { client, request } = usePublicApi()

async function loadCatalog(language?: string | null) {
  let result = await request(() => client.getSite(language ? { query: { lang: language } } : undefined))
  if (result.response.status === 400 && language) result = await request(() => client.getSite())
  if (!result.data?.messages['admin.sign_in']) throw publicError(503)
  return result.data
}

const { data: initialCatalog, error } = await useAsyncData('admin-ui-catalog', () => loadCatalog(preferredLanguage.value))
if (error.value || !initialCatalog.value) throw publicError(503)
const catalog = ref(initialCatalog.value)
const { t, locale, setLocaleMessage } = useI18n({
  useScope: 'local', inheritLocale: false, locale: catalog.value.language.code, fallbackLocale: false,
  flatJson: true, messages: { [catalog.value.language.code]: catalog.value.messages },
})

async function selectLanguage(event: Event) {
  const select = event.currentTarget
  if (!(select instanceof HTMLSelectElement) || changingLanguage.value) return
  if (!catalog.value.languages.some(language => language.code === select.value)) return
  changingLanguage.value = true
  catalogFailure.value = false
  try {
    const next = await loadCatalog(select.value)
    if (!active) return
    setLocaleMessage(next.language.code, next.messages)
    locale.value = next.language.code
    catalog.value = next
    preferredLanguage.value = next.language.code
  }
  catch {
    if (!active) return
    select.value = locale.value
    catalogFailure.value = true
  }
  finally {
    changingLanguage.value = false
  }
}

const themes = ['system', 'light', 'dark'] as const
const theme = ref<typeof themes[number]>('system')
const changingLanguage = ref(false)
const catalogFailure = ref(false)
const phase = ref<AdminSessionKind | null>(null)
const pending = ref(true)
const notice = ref('')
const settingsOpen = ref(false)
const contentOpen = ref(false)
const builderOpen = ref(false)
const accessView = ref<'account' | 'access' | null>(null)
const restrictedEntry = ref<RestrictedEntryMode | null>(null)
const accessSection = ref<'users' | 'roles' | 'audit'>('users')
const confirmingIdentity = ref(false)
const failure = ref('')
const limited = ref(false)
const login = ref('')
const password = ref('')
const code = ref('')
const heading = ref<HTMLElement | null>(null)
const csrf = ref('')
let retryTimer: ReturnType<typeof setTimeout> | undefined
let active = true

const activeTool = computed(() => {
  if (confirmingIdentity.value) return 'fresh_auth'
  if (settingsOpen.value) return 'settings'
  if (contentOpen.value) return 'content'
  if (builderOpen.value) return 'registry'
  return accessView.value ?? 'signed_in'
})
const title = computed(() => {
  if (restrictedEntry.value) return restrictedEntry.value === 'enrollment' ? 'entry_enrollment' : 'entry_recovery'
  switch (phase.value) {
    case 'preauth': return 'sign_in'
    case 'login': return 'two_step'
    case 'full': return activeTool.value
    case null: return pending.value ? 'checking' : 'name'
    default: return 'name'
  }
})
const blocked = computed(() => pending.value || limited.value)
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
    theme.value = themes.find(value => value === savedTheme) ?? 'system'
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
      settingsOpen.value = false
      contentOpen.value = false
      builderOpen.value = false
      accessView.value = null
      confirmingIdentity.value = false
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

async function submit(action: AdminAuthAction) {
  if (blocked.value) return
  pending.value = true
  notice.value = ''
  failure.value = ''
  try {
    const fields: Record<string, string> = {}
    if (action === 'login') {
      fields.login = login.value
      fields.password = password.value
    }
    if (action === 'mfa/verify' || action === 'step-up') fields.code = code.value
    if (action === 'step-up') fields.password = password.value
    password.value = ''
    code.value = ''
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

async function afterAuthAction(action: AdminAuthAction) {
  if (action === 'logout') {
    notice.value = 'signed_out'
    phase.value = null
    login.value = ''
    settingsOpen.value = false
    contentOpen.value = false
    builderOpen.value = false
    accessView.value = null
    confirmingIdentity.value = false
    await refresh()
  }
  else {
    phase.value = action === 'login' ? 'login' : 'full'
    if (action === 'mfa/verify') login.value = ''
    if (action === 'step-up') confirmingIdentity.value = false
  }
}

async function changeSettingsVisibility(open: boolean) {
  settingsOpen.value = open
  confirmingIdentity.value = false
  failure.value = ''
  await focusHeading()
}

async function changeContentVisibility(open: boolean) {
  contentOpen.value = open
  confirmingIdentity.value = false
  failure.value = ''
  await focusHeading()
}

async function changeBuilderVisibility(open: boolean) {
  builderOpen.value = open
  confirmingIdentity.value = false
  failure.value = ''
  await focusHeading()
}

async function changeAccessVisibility(view: typeof accessView.value) {
  accessView.value = view
  if (view === 'access') accessSection.value = 'users'
  confirmingIdentity.value = false
  failure.value = ''
  await focusHeading()
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

async function confirmActionIdentity() {
  confirmingIdentity.value = true
  failure.value = ''
  await focusHeading()
}

async function refreshPublishedCatalog() {
  try {
    const next = await loadCatalog(locale.value)
    if (!active) return
    setLocaleMessage(next.language.code, next.messages)
    locale.value = next.language.code
    catalog.value = next
    preferredLanguage.value = next.language.code
    catalogFailure.value = false
  }
  catch {
    if (active) catalogFailure.value = true
  }
}

onMounted(() => {
  loadPreferences()
  void refresh()
})
onBeforeUnmount(() => {
  active = false
  csrf.value = ''
  if (retryTimer) clearTimeout(retryTimer)
})
</script>

<template>
  <main class="admin-shell">
    <header class="admin-header">
      <span class="admin-name">{{ catalog.settings.title }} · {{ t('admin.name') }}</span>
      <div class="admin-preferences">
        <div>
          <label for="admin-language">{{ t('admin.language') }}</label>
          <select id="admin-language" :value="locale" :disabled="changingLanguage" @change="selectLanguage">
            <option v-for="language in catalog.languages" :key="language.code" :value="language.code" :lang="language.code" :dir="language.direction">
              {{ language.native_name }}
            </option>
          </select>
        </div>
        <div>
          <label for="admin-theme">{{ t('admin.theme') }}</label>
          <select id="admin-theme" v-model="theme" @change="savePreference('admin-theme', theme)">
            <option v-for="mode in themes" :key="mode" :value="mode">{{ t(`admin.${mode}`) }}</option>
          </select>
        </div>
      </div>
    </header>

    <section
      class="admin-card"
      :class="{ 'admin-card--wide': (settingsOpen || builderOpen || accessView) && !confirmingIdentity, 'admin-card--workspace': contentOpen && !confirmingIdentity }"
      aria-labelledby="admin-heading"
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
      <form v-else-if="phase === 'preauth'" :aria-busy="pending" :aria-describedby="failure ? 'admin-auth-error' : undefined" @submit.prevent="submit('login')">
        <label for="admin-login">{{ t('admin.login') }}</label>
        <input id="admin-login" v-model="login" type="text" required autocomplete="username" spellcheck="false">
        <label for="admin-password">{{ t('admin.password') }}</label>
        <input id="admin-password" v-model="password" type="password" required autocomplete="current-password">
        <button type="submit" :disabled="blocked">{{ t('admin.continue') }}</button>
        <button type="button" class="admin-secondary" :disabled="blocked" @click="changeRestrictedEntry('enrollment')">{{ t('admin.entry_enrollment') }}</button>
        <button type="button" class="admin-secondary" :disabled="blocked" @click="changeRestrictedEntry('recovery')">{{ t('admin.entry_recovery') }}</button>
      </form>

      <form v-else-if="phase === 'login'" :aria-busy="pending" @submit.prevent="submit('mfa/verify')">
        <p id="admin-code-help">{{ t('admin.code_help') }}</p>
        <label for="admin-code">{{ t('admin.code') }}</label>
        <input
          id="admin-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required
          :aria-describedby="failure ? 'admin-code-help admin-auth-error' : 'admin-code-help'"
        >
        <button type="submit" :disabled="blocked">{{ t('admin.verify') }}</button>
        <button type="button" class="admin-secondary" :disabled="blocked" @click="submit('logout')">{{ t('admin.restart') }}</button>
      </form>

      <template v-else-if="phase === 'full'">
        <form v-if="confirmingIdentity" :aria-busy="pending" @submit.prevent="submit('step-up')">
          <label for="admin-step-up-password">{{ t('admin.password') }}</label>
          <input id="admin-step-up-password" v-model="password" type="password" required autocomplete="current-password">
          <label for="admin-step-up-code">{{ t('admin.code') }}</label>
          <input id="admin-step-up-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required>
          <button type="submit" :disabled="blocked">{{ t('admin.unlock') }}</button>
          <button type="button" class="admin-secondary" :disabled="pending" @click="confirmingIdentity = false">{{ t('admin.cancel') }}</button>
        </form>
        <PortalSettings
          v-if="settingsOpen" v-show="!confirmingIdentity"
          :csrf="csrf" :translate="t" @published="refreshPublishedCatalog"
          @step-up="confirmActionIdentity" @session-changed="refresh(true)" @close="changeSettingsVisibility(false)"
        />
        <AdminContentWorkspace
          v-if="contentOpen" v-show="!confirmingIdentity" :csrf="csrf" :ui-language="locale" :translate="t"
          @step-up="confirmActionIdentity" @session-changed="refresh(true)" @close="changeContentVisibility(false)"
        />
        <AdminTypeBuilder
          v-if="builderOpen" v-show="!confirmingIdentity" :csrf="csrf" :ui-language="locale" :translate="t"
          @step-up="confirmActionIdentity" @session-changed="refresh(true)" @close="changeBuilderVisibility(false)"
        />
        <AdminAccount
          v-if="accessView === 'account'" v-show="!confirmingIdentity" :csrf="csrf" :ui-language="locale" :translate="t"
          @step-up="confirmActionIdentity" @session-changed="refresh(true)" @close="changeAccessVisibility(null)"
        />
        <section v-if="accessView === 'access'" v-show="!confirmingIdentity" class="access-navigation">
          <nav :aria-label="t('admin.access')" class="access-navigation">
            <button type="button" class="admin-secondary" :aria-pressed="accessSection === 'users'" @click="accessSection = 'users'">{{ t('admin.users') }}</button>
            <button type="button" class="admin-secondary" :aria-pressed="accessSection === 'roles'" @click="accessSection = 'roles'">{{ t('admin.roles') }}</button>
            <button type="button" class="admin-secondary" :aria-pressed="accessSection === 'audit'" @click="accessSection = 'audit'">{{ t('admin.audit') }}</button>
            <button type="button" class="admin-secondary" @click="changeAccessVisibility(null)">{{ t('admin.close') }}</button>
          </nav>
          <AdminUsers v-if="accessSection === 'users'" :csrf="csrf" :ui-language="locale" :translate="t" @step-up="confirmActionIdentity" @session-changed="refresh(true)" />
          <AdminRoles v-else-if="accessSection === 'roles'" :csrf="csrf" :translate="t" @step-up="confirmActionIdentity" @session-changed="refresh(true)" />
          <AdminAudit v-else :ui-language="locale" :translate="t" @session-changed="refresh(true)" />
        </section>
        <template v-if="!settingsOpen && !contentOpen && !builderOpen && !accessView && !confirmingIdentity">
          <button type="button" :disabled="blocked" @click="changeContentVisibility(true)">{{ t('admin.content') }}</button>
          <button type="button" :disabled="blocked" @click="changeBuilderVisibility(true)">{{ t('admin.registry') }}</button>
          <button type="button" :disabled="blocked" @click="changeSettingsVisibility(true)">{{ t('admin.settings') }}</button>
          <button type="button" :disabled="blocked" @click="changeAccessVisibility('account')">{{ t('admin.account') }}</button>
          <button type="button" :disabled="blocked" @click="changeAccessVisibility('access')">{{ t('admin.access') }}</button>
          <button type="button" class="admin-secondary" :disabled="blocked" @click="submit('logout')">{{ t('admin.sign_out') }}</button>
        </template>
      </template>
      <template v-else-if="phase">
        <p>{{ t('admin.restricted') }}</p>
        <button type="button" :disabled="blocked" @click="submit('logout')">{{ t('admin.restart') }}</button>
      </template>
      <button v-else-if="!pending" type="button" :disabled="limited" @click="refresh(true)">{{ t('admin.retry') }}</button>
    </section>
  </main>
</template>

<style scoped>
@import '../assets/admin-sign-in.css';
</style>
