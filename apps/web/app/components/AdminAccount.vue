<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { accountPageLimit, changePassword, completeFactor, hasLaterAccountPage, listSessions, prepareFactor, readProfile, regenerateCodes, revokeAllSessions, revokeSession } from '~/utils/admin-account'
import type { AccountProfile, AccountSession, FactorPreparation } from '~/utils/admin-account'
import { AdminContentError } from '~/utils/admin-request'

const props = defineProps<{ csrf: string, uiLanguage: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [], 'editing-state': [state: { dirty: boolean, busy: boolean }] }>()

const profile = ref<AccountProfile>()
const sessions = ref<AccountSession[]>([])
const offset = ref(0)
const busy = ref(false)
const errorKey = ref('')
const confirmation = ref<'password' | 'factor' | 'codes' | 'all' | AccountSession>()
const factor = ref<FactorPreparation>()
const codes = ref<string[]>([])
const password = ref('')
const code = ref('')
watch([() => Boolean(password.value || code.value || factor.value || codes.value.length), busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const sensitiveHeading = ref<HTMLElement>()
let active = true
let sessionsNeedRefresh = false

function clearFactor() {
  factor.value = undefined
  code.value = ''
}

watch([() => props.csrf, busy], ([token, working], [previousToken]) => {
  if (token !== previousToken) {
    clearFactor()
    sessionsNeedRefresh = Boolean(token)
  }
  if (!working && sessionsNeedRefresh && profile.value) {
    sessionsNeedRefresh = false
    void run(() => loadSessions(offset.value))
  }
})
onBeforeUnmount(() => {
  active = false
  clearFactor()
  password.value = ''
  codes.value = []
})

function showError(error: unknown) {
  errorKey.value = 'admin.unavailable'
  if (!(error instanceof AdminContentError)) return
  if (error.code === 'step_up_required') {
    if (factor.value) clearFactor()
    errorKey.value = ''
    emit('step-up')
  } else if (error.code === 'invalid_credentials') {
    errorKey.value = 'admin.invalid_code'
  } else if (error.status === 401 || error.code === 'csrf_failed') {
    emit('session-changed')
  } else if (error.status === 400) {
    errorKey.value = 'admin.account_validation'
  } else if (error.status === 404 || error.status === 409) {
    errorKey.value = 'admin.changed'
  } else if (error.status === 429) {
    errorKey.value = 'admin.limited'
  }
}

async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  errorKey.value = ''
  try { await action() }
  catch (error: unknown) { if (active) showError(error) }
  finally { busy.value = false }
}

async function loadSessions(next: number) {
  const token = props.csrf
  const out = await listSessions(next)
  if (!active || props.csrf !== token) return
  sessions.value = out.items
  offset.value = next
  if (typeof confirmation.value === 'object' && confirmation.value.current) {
    confirmation.value = out.items.find(session => session.current)
  }
}

function load() {
  return run(async () => {
    profile.value = await readProfile()
    await loadSessions(0)
  })
}

function date(value: string) {
  return Intl.DateTimeFormat(props.uiLanguage, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

async function showCodes(value: string[]) {
  if (!active) return
  codes.value = value
  clearFactor()
  confirmation.value = undefined
  await nextTick()
  sensitiveHeading.value?.focus()
}

function finishCodes() {
  codes.value = []
  emit('session-changed')
}

function selectAction(action: NonNullable<typeof confirmation.value>) {
  confirmation.value = action
  password.value = ''
  clearFactor()
}

async function updateFactor() {
  const prepared = factor.value
  if (!prepared) {
    const pending = await prepareFactor(props.csrf)
    if (active) factor.value = pending
    return
  }
  const entered = code.value
  code.value = ''
  const out = await completeFactor(prepared.replacement_token, entered, props.csrf)
  await showCodes(out.recovery_codes)
}

async function confirmAction() {
  const action = confirmation.value
  if (!action) return
  await run(async () => {
    switch (action) {
      case 'password': {
        await changePassword(password.value, props.csrf)
        password.value = ''
        if (active) emit('session-changed')
        return
      }
      case 'factor':
        await updateFactor()
        return
      case 'codes':
        await showCodes((await regenerateCodes(props.csrf)).recovery_codes)
        return
      case 'all':
        await revokeAllSessions(props.csrf)
        if (active) emit('session-changed')
        return
      default:
        await revokeSession(action.id, props.csrf)
        if (!active) return
        if (action.current) emit('session-changed')
        else {
          confirmation.value = undefined
          await loadSessions(offset.value)
        }
    }
  })
}

function cancel() {
  password.value = ''
  clearFactor()
  confirmation.value = undefined
  errorKey.value = ''
}

onMounted(() => { void load() })
</script>

<template>
  <div class="access-panel" :aria-busy="busy">
    <template v-if="codes.length">
      <h2 ref="sensitiveHeading" tabindex="-1">{{ translate('admin.account_codes') }}</h2>
      <p>{{ translate('admin.account_codes_help') }}</p>
      <ul class="access-codes">
        <li v-for="recoveryCode in codes" :key="recoveryCode"><code>{{ recoveryCode }}</code></li>
      </ul>
      <button type="button" @click="finishCodes">{{ translate('admin.account_done') }}</button>
    </template>
    <template v-else>
      <p v-if="errorKey" role="alert">{{ translate(errorKey) }}</p>
      <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
      <button v-if="!profile && errorKey" type="button" :disabled="busy" @click="load">{{ translate('admin.retry') }}</button>
      <template v-if="profile">
        <h2>{{ profile.login }}</h2>
        <p>{{ translate('admin.account_codes_remaining') }} {{ profile.recovery_codes_remaining }}</p>
        <div class="access-actions">
          <button type="button" class="admin-secondary" :disabled="busy" @click="selectAction('password')">{{ translate('admin.account_change_password') }}</button>
          <button type="button" class="admin-secondary" :disabled="busy || !profile.mfa_enabled" @click="selectAction('factor')">{{ translate('admin.account_replace_factor') }}</button>
          <button type="button" class="admin-secondary" :disabled="busy" @click="selectAction('codes')">{{ translate('admin.account_regenerate_codes') }}</button>
        </div>

        <form v-if="confirmation" @submit.prevent="confirmAction">
          <template v-if="typeof confirmation === 'string'">
            <p>{{ translate(confirmation === 'codes' ? 'admin.account_replace_codes_warning' : 'admin.account_logout_warning') }}</p>
            <template v-if="confirmation === 'password'">
              <label for="account-new-password">{{ translate('admin.account_new_password') }}</label>
              <input id="account-new-password" v-model="password" type="password" required autocomplete="new-password">
            </template>
            <template v-if="factor">
              <p>{{ translate('admin.account_factor_help') }}</p>
              <code>{{ factor.secret }}</code>
              <label for="account-factor-code">{{ translate('admin.code') }}</label>
              <input id="account-factor-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" required pattern="[0-9]{6}" maxlength="6">
            </template>
          </template>
          <p v-else>{{ translate('admin.account_revoke_confirmation') }} {{ date(confirmation.created_at) }}</p>
          <div class="access-actions">
            <button type="submit" :disabled="busy">{{ translate(confirmation === 'factor' && !factor ? 'admin.continue' : 'admin.verify') }}</button>
            <button type="button" class="admin-secondary" :disabled="busy" @click="cancel">{{ translate('admin.cancel') }}</button>
          </div>
        </form>

        <section aria-labelledby="account-sessions-heading">
          <h2 id="account-sessions-heading">{{ translate('admin.account_sessions') }}</h2>
          <ul class="access-rows">
            <li v-for="session in sessions" :key="session.id">
              <strong v-if="session.current">{{ translate('admin.account_current_session') }}</strong>
              <dl>
                <div><dt>{{ translate('admin.account_started') }}</dt><dd>{{ date(session.created_at) }}</dd></div>
                <div><dt>{{ translate('admin.account_last_seen') }}</dt><dd>{{ date(session.last_seen) }}</dd></div>
                <div><dt>{{ translate('admin.account_expires') }}</dt><dd>{{ date(session.expires_at) }}</dd></div>
              </dl>
              <button type="button" class="admin-secondary" :disabled="busy" @click="selectAction(session)">
                {{ translate('admin.account_revoke_session') }}
              </button>
            </li>
          </ul>
          <div class="access-actions">
            <button v-if="offset" type="button" class="admin-secondary" :disabled="busy" @click="run(() => loadSessions(offset - accountPageLimit))">{{ translate('previous') }}</button>
            <button v-if="hasLaterAccountPage(offset, sessions.length)" type="button" class="admin-secondary" :disabled="busy" @click="run(() => loadSessions(offset + accountPageLimit))">{{ translate('next') }}</button>
            <button type="button" class="admin-secondary" :disabled="busy" @click="selectAction('all')">{{ translate('admin.account_revoke_all') }}</button>
          </div>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
