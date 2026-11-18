<script setup lang="ts">
import { useHead, useI18n, useResponseHeader } from '#imports'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import en from '../../i18n/admin-en.json'
import kk from '../../i18n/admin-kk.json'
import pl from '../../i18n/admin-pl.json'
import ru from '../../i18n/admin-ru.json'
import { CmsAuthError, restoreCmsSession, submitCmsAuth } from '~/utils/cms-auth'
import type { CmsSessionKind } from '~/utils/cms-auth'

defineOptions({ name: 'CmsSignInPage' })

const { t, locale } = useI18n({
  useScope: 'local', inheritLocale: false, locale: 'pl', fallbackLocale: 'pl',
  messages: { en, kk, pl, ru },
})
const themes = ['system', 'light', 'dark'] as const
const theme = ref<typeof themes[number]>('system')
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
    case 'preauth': return 'signIn'
    case 'login': return 'twoStep'
    case 'full': return 'signedIn'
    case null: return pending.value ? 'checking' : 'cms'
    default: return 'cms'
  }
})
const blocked = computed(() => pending.value || limited.value)
useHead(() => ({
  title: phase.value ? `${t(title.value)} · CMS` : t('cms'),
  htmlAttrs: { lang: locale.value, dir: 'ltr', 'data-cms-theme': theme.value },
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
    const savedLocale = localStorage.getItem('cms-language')
    if (savedLocale === 'pl' || savedLocale === 'en' || savedLocale === 'ru' || savedLocale === 'kk') locale.value = savedLocale
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
    failure.value = action === 'mfa/verify' ? 'invalidCode' : 'invalidCredentials'
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
      notice.value = 'signedOut'
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
      <span class="cms-name">Stepolonia CMS</span>
      <div class="cms-preferences">
        <div>
          <label for="cms-language">{{ t('language') }}</label>
          <select id="cms-language" v-model="locale" @change="savePreference('cms-language', locale)">
            <option value="pl" lang="pl">Polski</option>
            <option value="en" lang="en">English</option>
            <option value="ru" lang="ru">Русский</option>
            <option value="kk" lang="kk">Қазақша</option>
          </select>
        </div>
        <div>
          <label for="cms-theme">{{ t('theme') }}</label>
          <select id="cms-theme" v-model="theme" @change="savePreference('cms-theme', theme)">
            <option v-for="mode in themes" :key="mode" :value="mode">{{ t(mode) }}</option>
          </select>
        </div>
      </div>
    </header>

    <section class="cms-card" aria-labelledby="cms-heading">
      <h1 id="cms-heading" ref="heading" tabindex="-1">{{ t(title) }}</h1>
      <output v-if="notice" class="cms-status">{{ t(notice) }}</output>
      <p v-if="failure" id="cms-auth-error" class="cms-error" role="alert">{{ t(failure) }}</p>
      <output v-if="pending && phase !== null" class="cms-status">{{ t('working') }}</output>

      <form v-if="phase === 'preauth'" :aria-busy="pending" :aria-describedby="failure ? 'cms-auth-error' : undefined" @submit.prevent="submit('login')">
        <label for="cms-login">{{ t('login') }}</label>
        <input id="cms-login" v-model="login" type="text" required autocomplete="username" spellcheck="false">
        <label for="cms-password">{{ t('password') }}</label>
        <input id="cms-password" v-model="password" type="password" required autocomplete="current-password">
        <button type="submit" :disabled="blocked">{{ t('continue') }}</button>
      </form>

      <form v-else-if="phase === 'login'" :aria-busy="pending" @submit.prevent="submit('mfa/verify')">
        <p id="cms-code-help">{{ t('codeHelp') }}</p>
        <label for="cms-code">{{ t('code') }}</label>
        <input
          id="cms-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required
          :aria-describedby="failure ? 'cms-code-help cms-auth-error' : 'cms-code-help'"
        >
        <button type="submit" :disabled="blocked">{{ t('verify') }}</button>
        <button type="button" class="cms-secondary" :disabled="blocked" @click="submit('logout')">{{ t('restart') }}</button>
      </form>

      <button v-else-if="phase === 'full'" type="button" :disabled="blocked" @click="submit('logout')">{{ t('signOut') }}</button>
      <template v-else-if="phase">
        <p>{{ t('restricted') }}</p>
        <button type="button" :disabled="blocked" @click="submit('logout')">{{ t('restart') }}</button>
      </template>
      <button v-else-if="!pending" type="button" :disabled="limited" @click="refresh(true)">{{ t('retry') }}</button>
    </section>
  </main>
</template>

<style scoped>
@import '../assets/cms-sign-in.css';
</style>
