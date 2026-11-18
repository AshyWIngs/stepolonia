<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { accountPageLimit, hasLaterAccountPage, listSessions, revokeSession } from '~/utils/admin-account'
import type { AccountSession } from '~/utils/admin-account'
import { accessNotice } from '~/utils/admin-access-feedback'

const props = defineProps<{ userId: string, csrf: string, uiLanguage: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [] }>()
const selected = ref<AccountSession>()
const sessions = ref<AccountSession[]>([])
const offset = ref(0)
const busy = ref(false)
const error = ref('')
let active = true

async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try { await work() }
  catch (problem: unknown) {
    if (!active) return
    error.value = accessNotice(problem, () => { emit('step-up') }, () => { emit('session-changed') })
  } finally { busy.value = false }
}

async function load(next: number) {
  const result = await listSessions(next, props.userId)
  if (!active) return
  sessions.value = result.items
  offset.value = next
}

async function revoke() {
  const session = selected.value
  if (!session) return
  await run(async () => {
    await revokeSession(session.id, props.csrf)
    if (!active) return
    selected.value = undefined
    if (session.current) emit('session-changed')
    else await load(offset.value)
  })
}

function date(value: string) {
  return Intl.DateTimeFormat(props.uiLanguage, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

watch(() => props.csrf, () => { if (!sessions.value.length) void run(() => load(offset.value)) })
onMounted(() => { void run(() => load(0)) })
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="access-panel" :aria-busy="busy">
    <p v-if="error" role="alert">{{ translate(error) }}</p>
    <p v-else-if="busy" role="status">{{ translate('admin.working') }}</p>
    <p v-else-if="!sessions.length">{{ translate('admin.account_no_sessions') }}</p>
    <ul v-if="!error || selected" class="access-rows">
      <li v-for="session in sessions" :key="session.id">
        <strong v-if="session.current">{{ translate('admin.account_current_session') }}</strong>
        <dl>
          <div><dt>{{ translate('admin.account_started') }}</dt><dd>{{ date(session.created_at) }}</dd></div>
          <div><dt>{{ translate('admin.account_last_seen') }}</dt><dd>{{ date(session.last_seen) }}</dd></div>
          <div><dt>{{ translate('admin.account_expires') }}</dt><dd>{{ date(session.expires_at) }}</dd></div>
        </dl>
        <button type="button" :disabled="busy" @click="selected = session">{{ translate('admin.account_revoke_session') }}</button>
      </li>
    </ul>
    <div v-if="selected" class="access-confirm">
      <p>{{ translate('admin.account_revoke_confirmation') }} {{ date(selected.created_at) }}</p>
      <div class="access-actions">
        <button type="button" :disabled="busy" @click="revoke">{{ translate('admin.verify') }}</button>
        <button type="button" :disabled="busy" @click="selected = undefined">{{ translate('admin.cancel') }}</button>
      </div>
    </div>
    <div class="access-actions">
      <button v-if="error" type="button" :disabled="busy" @click="run(() => load(offset))">{{ translate('admin.access_reload') }}</button>
      <button v-if="offset" type="button" :disabled="busy" @click="run(() => load(offset - accountPageLimit))">{{ translate('previous') }}</button>
      <button v-if="hasLaterAccountPage(offset, sessions.length)" type="button" :disabled="busy" @click="run(() => load(offset + accountPageLimit))">{{ translate('next') }}</button>
    </div>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
