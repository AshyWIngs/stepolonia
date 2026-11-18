<script setup lang="ts">
import { useAsyncData, useCookie, useHeadSafe, useI18n, useResponseHeader } from '#imports'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { CmsAuthError, restoreCmsSession, submitCmsAuth } from '~/utils/cms-auth'
import type { CmsSessionKind } from '~/utils/cms-auth'
import { usePublicApi } from '~/composables/usePublicApi'
import { publicError } from '~/utils/public-content'

defineOptions({ name: 'CmsSignInPage' })

const preferredLanguage = useCookie<string | null>('cms-language', { sameSite: 'lax', secure: true, maxAge: 31536000 })
const { client, request } = usePublicApi()

async function loadCatalog(language?: string | null) {
  let result = await request(() => client.getSite(language ? { query: { lang: language } } : undefined))
  if (result.response.status === 400 && language) result = await request(() => client.getSite())
  if (!result.data?.messages['cms.sign_in']) throw publicError(503)
  return result.data
}

const { data: initialCatalog, error } = await useAsyncData('cms-ui-catalog', () => loadCatalog(preferredLanguage.value))
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
const phase = ref<CmsSessionKind | null>(null)
const pending = ref(true)
const notice = ref('')
const failure = ref('')
const limited = ref(false)
const login = ref('')
const password = ref('')
const code = ref('')
const heading = ref<HTMLElement | null>(null)
let csrf = ''
let retryTimer: ReturnType<typeof setTimeout> | undefined
let active = true

const title = computed(() => {
  switch (phase.value) {
    case 'preauth': return 'sign_in'
    case 'login': return 'two_step'
    case 'full': return 'signed_in'
    case null: return pending.value ? 'checking' : 'cms'
    default: return 'cms'
  }
})
const blocked = computed(() => pending.value || limited.value)
const localizedTitle = computed(() => t(`cms.${title.value}`))
useHeadSafe(() => ({
  title: `${localizedTitle.value} · ${catalog.value.settings.title}`,
  htmlAttrs: { lang: locale.value, dir: catalog.value.language.direction, 'data-cms-theme': theme.value },
}))
useResponseHeader('Cache-Control').value = 'no-store'

function savePreference(key: string, value: string) {
  try { localStorage.setItem(key, value) }
  catch { /* The preference still applies for this visit. */ }
}

function loadPreferences() {
  try {
    const savedTheme = localStorage.getItem('cms-theme')
    theme.value = themes.find(value => value === savedTheme) ?? 'system'
  }
  catch { /* Preferences are optional when browser storage is unavailable. */ }
}

async function focusHeading() {
  await nextTick()
  heading.value?.focus()
}

function handleFailure(error: unknown, action?: 'login' | 'mfa/verify' | 'logout') {
  if (error instanceof CmsAuthError && error.status === 429) {
    failure.value = 'limited'
    limited.value = true
    retryTimer = setTimeout(() => { limited.value = false }, Math.min(error.retryAfter ?? 900, 86400) * 1000)
    return
  }
  if (error instanceof CmsAuthError && error.code === 'invalid_credentials') {
    failure.value = action === 'mfa/verify' ? 'invalid_code' : 'invalid_credentials'
    return
  }
  // A failed response may follow a successful cookie or token rotation.
  csrf = ''
  phase.value = null
  failure.value = error instanceof CmsAuthError && (error.status === 401 || error.status === 403)
    ? 'changed' : 'unavailable'
}

async function refresh(moveFocus = false) {
  pending.value = true
  failure.value = ''
  try {
    const restored = await restoreCmsSession()
    if (!active) return
    csrf = restored.csrf
    phase.value = restored.kind
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    pending.value = false
  }
  if (moveFocus) await focusHeading()
}

async function submit(action: 'login' | 'mfa/verify' | 'logout') {
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
    if (action === 'mfa/verify') fields.code = code.value
    password.value = ''
    code.value = ''
    csrf = await submitCmsAuth(action, csrf, fields)
    if (!active) return
    if (action === 'logout') {
      notice.value = 'signed_out'
      phase.value = null
      login.value = ''
      await refresh()
    }
    else {
      phase.value = action === 'login' ? 'login' : 'full'
      if (action === 'mfa/verify') login.value = ''
    }
    await focusHeading()
  }
  catch (error: unknown) {
    if (active) handleFailure(error, action)
  }
  finally {
    pending.value = false
  }
}

onMounted(() => {
  loadPreferences()
  void refresh()
})
onBeforeUnmount(() => {
  active = false
  csrf = ''
  if (retryTimer) clearTimeout(retryTimer)
})
</script>

<template>
  <main class="cms-shell">
    <header class="cms-header">
      <span class="cms-name">{{ catalog.settings.title }} · {{ t('cms.cms') }}</span>
      <div class="cms-preferences">
        <div>
          <label for="cms-language">{{ t('cms.language') }}</label>
          <select id="cms-language" :value="locale" :disabled="changingLanguage" @change="selectLanguage">
            <option v-for="language in catalog.languages" :key="language.code" :value="language.code" :lang="language.code" :dir="language.direction">
              {{ language.native_name }}
            </option>
          </select>
        </div>
        <div>
          <label for="cms-theme">{{ t('cms.theme') }}</label>
          <select id="cms-theme" v-model="theme" @change="savePreference('cms-theme', theme)">
            <option v-for="mode in themes" :key="mode" :value="mode">{{ t(`cms.${mode}`) }}</option>
          </select>
        </div>
      </div>
    </header>

    <section class="cms-card" aria-labelledby="cms-heading">
      <h1 id="cms-heading" ref="heading" tabindex="-1">{{ localizedTitle }}</h1>
      <output v-if="notice" class="cms-status">{{ t(`cms.${notice}`) }}</output>
      <p v-if="failure" id="cms-auth-error" class="cms-error" role="alert">{{ t(`cms.${failure}`) }}</p>
      <p v-if="catalogFailure" class="cms-error" role="alert">{{ t('cms.unavailable') }}</p>
      <output v-if="pending && phase !== null" class="cms-status">{{ t('cms.working') }}</output>

      <form v-if="phase === 'preauth'" :aria-busy="pending" :aria-describedby="failure ? 'cms-auth-error' : undefined" @submit.prevent="submit('login')">
        <label for="cms-login">{{ t('cms.login') }}</label>
        <input id="cms-login" v-model="login" type="text" required autocomplete="username" spellcheck="false">
        <label for="cms-password">{{ t('cms.password') }}</label>
        <input id="cms-password" v-model="password" type="password" required autocomplete="current-password">
        <button type="submit" :disabled="blocked">{{ t('cms.continue') }}</button>
      </form>

      <form v-else-if="phase === 'login'" :aria-busy="pending" @submit.prevent="submit('mfa/verify')">
        <p id="cms-code-help">{{ t('cms.code_help') }}</p>
        <label for="cms-code">{{ t('cms.code') }}</label>
        <input
          id="cms-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required
          :aria-describedby="failure ? 'cms-code-help cms-auth-error' : 'cms-code-help'"
        >
        <button type="submit" :disabled="blocked">{{ t('cms.verify') }}</button>
        <button type="button" class="cms-secondary" :disabled="blocked" @click="submit('logout')">{{ t('cms.restart') }}</button>
      </form>

      <button v-else-if="phase === 'full'" type="button" :disabled="blocked" @click="submit('logout')">{{ t('cms.sign_out') }}</button>
      <template v-else-if="phase">
        <p>{{ t('cms.restricted') }}</p>
        <button type="button" :disabled="blocked" @click="submit('logout')">{{ t('cms.restart') }}</button>
      </template>
      <button v-else-if="!pending" type="button" :disabled="limited" @click="refresh(true)">{{ t('cms.retry') }}</button>
    </section>
  </main>
</template>

<style scoped>
@import '../assets/cms-sign-in.css';
</style>
