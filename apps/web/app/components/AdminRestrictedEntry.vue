<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { AdminAuthError, finishRestrictedEntry, startRestrictedEntry } from '~/utils/admin-auth'
import type { AdminSessionKind, RestrictedEntryMode } from '~/utils/admin-auth'

const props = defineProps<{ mode: RestrictedEntryMode, phase: AdminSessionKind | null, csrf: string, translate: (key: string) => string }>()
const emit = defineEmits<{ started: [csrf: string], finished: [], restart: [] }>()
const busy = ref(false)
const login = ref('')
const password = ref('')
const invitation = ref('')
const recoveryCode = ref('')
const code = ref('')
const secret = ref('')
const codes = ref<string[]>([])
const failure = ref('')
const focusTarget = ref<HTMLElement>()
let active = true

onBeforeUnmount(() => {
  active = false
  clearInput()
  secret.value = ''
  codes.value = []
})

function clearInput() {
  login.value = ''
  password.value = ''
  invitation.value = ''
  recoveryCode.value = ''
  code.value = ''
}

function explainFailure(error: unknown) {
  if (!(error instanceof AdminAuthError)) return 'admin.unavailable'
  if (error.code === 'invalid_credentials') return 'admin.entry_invalid'
  if (error.status === 429) return 'admin.limited'
  if (error.status === 401 || error.code === 'csrf_failed') return 'admin.changed'
  if (error.status === 400) return 'admin.account_validation'
  return 'admin.unavailable'
}

async function start() {
  const fields: Record<string, string> = props.mode === 'enrollment'
    ? { token: invitation.value, password: password.value }
    : { login: login.value, password: password.value, code: recoveryCode.value }
  clearInput()
  const entered = await startRestrictedEntry(props.mode, props.csrf, fields)
  if (!active) return
  secret.value = entered.secret
  emit('started', entered.csrf)
}

async function complete() {
  const entered = code.value
  code.value = ''
  const out = await finishRestrictedEntry(props.mode, props.csrf, entered)
  if (!active) return
  secret.value = ''
  codes.value = out
  emit('finished')
}

async function submit() {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  try {
    await (props.phase === 'preauth' ? start() : complete())
    await nextTick()
    if (active) focusTarget.value?.focus()
  } catch (error: unknown) {
    if (active) failure.value = explainFailure(error)
  } finally { busy.value = false }
}
</script>

<template>
  <div class="access-panel" :aria-busy="busy">
    <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <template v-if="codes.length">
      <h2 ref="focusTarget" tabindex="-1">{{ translate('admin.account_codes') }}</h2>
      <p>{{ translate(mode === 'enrollment' ? 'admin.entry_activation' : 'admin.account_codes_help') }}</p>
      <ul class="access-codes">
        <li v-for="value in codes" :key="value"><code>{{ value }}</code></li>
      </ul>
      <button type="button" @click="emit('restart')">{{ translate('admin.account_done') }}</button>
    </template>
    <template v-else>
      <form v-if="phase === 'preauth'" @submit.prevent="submit">
        <template v-if="mode === 'enrollment'">
          <label for="entry-invitation">{{ translate('admin.entry_invitation') }}</label>
          <input id="entry-invitation" v-model="invitation" required autocomplete="off" spellcheck="false">
        </template>
        <template v-else>
          <label for="entry-login">{{ translate('admin.login') }}</label>
          <input id="entry-login" v-model="login" required autocomplete="username">
          <label for="entry-recovery-code">{{ translate('admin.entry_recovery_code') }}</label>
          <input id="entry-recovery-code" v-model="recoveryCode" required autocomplete="off" spellcheck="false">
        </template>
        <label for="entry-password">{{ translate(mode === 'enrollment' ? 'admin.account_new_password' : 'admin.password') }}</label>
        <input id="entry-password" v-model="password" required type="password" :autocomplete="mode === 'enrollment' ? 'new-password' : 'current-password'">
        <button type="submit" :disabled="busy">{{ translate('admin.continue') }}</button>
      </form>
      <form v-else-if="secret" @submit.prevent="submit">
        <p>{{ translate('admin.account_factor_help') }}</p>
        <code>{{ secret }}</code>
        <label for="entry-mobile-code">{{ translate('admin.code') }}</label>
        <input id="entry-mobile-code" ref="focusTarget" v-model="code" inputmode="numeric" autocomplete="one-time-code" required maxlength="6" pattern="[0-9]{6}">
        <button type="submit" :disabled="busy">{{ translate('admin.verify') }}</button>
      </form>
      <p v-else>{{ translate('admin.entry_interrupted') }}</p>
      <button type="button" :disabled="busy" class="admin-secondary" @click="emit('restart')">{{ translate(phase === 'preauth' ? 'admin.cancel' : 'admin.restart') }}</button>
    </template>
  </div>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
