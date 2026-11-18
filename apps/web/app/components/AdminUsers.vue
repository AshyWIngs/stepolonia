<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { changeUserState, createAccessUser, issueInvitation, listAccessUsers, readMembership, replaceMembership } from '~/utils/admin-access'
import type { AccessUser, MembershipSet } from '~/utils/admin-access'
import { useAccessActions } from '~/utils/admin-access-feedback'

const props = defineProps<{ csrf: string, uiLanguage: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [] }>()
const users = ref<AccessUser[]>([])
const selected = ref<AccessUser>()
const membership = ref<MembershipSet>()
const chosen = ref<string[]>([])
const nextOffset = ref<number | null>(null)
const pageOffset = ref(0)
const sessionsOpen = ref(false)
const name = ref('')
const issue = ref<{ token: string, expires_at: string }>()
const panel = ref<HTMLElement>()
const confirmation = ref<'disable' | 'activate' | 'invite' | 'memberships'>()
const { active, busy, error, feedback, run } = useAccessActions(() => emit('step-up'), () => emit('session-changed'))
const additions = computed(() => chosen.value.filter(id => !membership.value?.role_ids.includes(id)).length)
const removals = computed(() => membership.value?.role_ids.filter(id => !chosen.value.includes(id)).length ?? 0)

function load(offset = 0) {
  return run(async () => {
    const page = await listAccessUsers(offset)
    if (!active.value) return
    users.value = page.items
    pageOffset.value = offset
    nextOffset.value = page.next_offset
  })
}

function updateUser(user: AccessUser) {
  selected.value = user
  const existing = users.value.some(item => item.id === user.id)
  users.value = existing ? users.value.map(item => item.id === user.id ? user : item) : [user, ...users.value]
}

async function select(user: AccessUser) {
  selected.value = user
  membership.value = undefined
  sessionsOpen.value = false
  chosen.value = []
  issue.value = undefined
  confirmation.value = undefined
  feedback.value = ''
  error.value = ''
  await nextTick()
  if (active.value) panel.value?.focus()
}

function create() {
  return run(async () => {
    const user = await createAccessUser(name.value, props.csrf)
    if (!active.value) return
    name.value = ''
    updateUser(user)
    await select(user)
    feedback.value = 'admin.users_created'
  })
}

async function loadMembership(user: AccessUser) {
  membership.value = undefined
  const found = await readMembership(user)
  if (!active.value || selected.value?.id !== user.id) return
  membership.value = found
  chosen.value = found.role_ids.slice()
}

async function saveUser(user: AccessUser, action: 'activate' | 'disable' | 'invite' | 'memberships') {
  switch (action) {
    case 'invite': {
      const result = await issueInvitation(user, props.csrf)
      if (!active.value) return
      updateUser(result.user)
      issue.value = { token: result.token, expires_at: result.expires_at }
      return
    }
    case 'memberships': {
      const current = membership.value
      if (!current) return
      const newUser = await replaceMembership(current, chosen.value, props.csrf)
      if (!active.value) return
      updateUser(newUser)
      await loadMembership(newUser)
      return
    }
    default: {
      const updated = await changeUserState(user, action, props.csrf)
      if (!active.value) return
      updateUser(updated)
      membership.value = undefined
      sessionsOpen.value = false
    }
  }
}

function openMembership() {
  const user = selected.value
  if (user) return run(() => loadMembership(user))
}

function confirm() {
  const user = selected.value
  const action = confirmation.value
  if (!user || !action) return
  return run(async () => {
    await saveUser(user, action)
    if (!active.value) return
    confirmation.value = undefined
    feedback.value = 'admin.access_saved'
  })
}

function reset() {
  issue.value = undefined
  selected.value = undefined
  sessionsOpen.value = false
  membership.value = undefined
  confirmation.value = undefined
  void load()
}

function date(value: string) {
  return Intl.DateTimeFormat(props.uiLanguage, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

onMounted(() => { void load() })
onBeforeUnmount(() => { issue.value = undefined })
</script>

<template>
  <section class="access-panel" :aria-busy="busy">
    <h2>{{ translate('admin.users') }}</h2>
    <p v-if="error" role="alert">{{ translate(error) }}</p>
    <p v-if="feedback" role="status">{{ translate(feedback) }}</p>
    <form @submit.prevent="create">
      <label for="access-new-login">{{ translate('admin.login') }}</label>
      <input id="access-new-login" v-model="name" :disabled="busy" required maxlength="256">
      <button type="submit" :disabled="busy">{{ translate('admin.users_create') }}</button>
    </form>
    <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
    <ul class="access-rows">
      <li v-for="user in users" :key="user.id">
        <button type="button" :disabled="busy" :aria-pressed="selected?.id === user.id" @click="select(user)">{{ user.login }}</button>
        · {{ translate(`admin.access_state_${user.state}`) }}
      </li>
    </ul>
    <div class="access-actions">
      <button v-if="pageOffset" type="button" :disabled="busy" @click="load(Math.max(0, pageOffset - 20))">{{ translate('previous') }}</button>
      <button v-if="nextOffset !== null" type="button" :disabled="busy" @click="load(nextOffset)">{{ translate('next') }}</button>
      <button type="button" :disabled="busy" @click="reset">{{ translate('admin.access_reload') }}</button>
    </div>
    <section v-if="selected" aria-labelledby="access-target-account">
      <h3 id="access-target-account" ref="panel" tabindex="-1">{{ selected.login }}</h3>
      <p>{{ translate(`admin.access_state_${selected.state}`) }}</p>
      <div class="access-actions">
        <button v-if="selected.state === 'pending'" type="button" :disabled="busy" @click="confirmation = 'invite'">{{ translate('admin.users_issue_invite') }}</button>
        <template v-else>
          <button v-if="!membership" type="button" :disabled="busy" @click="openMembership">{{ translate('admin.users_manage_memberships') }}</button>
          <button v-if="selected.state === 'active'" type="button" :disabled="busy" :aria-expanded="sessionsOpen" @click="sessionsOpen = !sessionsOpen">{{ translate('admin.account_sessions') }}</button>
          <button type="button" :disabled="busy" @click="confirmation = selected.state === 'active' ? 'disable' : 'activate'">
            {{ translate(selected.state === 'active' ? 'admin.access_disable' : 'admin.users_activate') }}
          </button>
        </template>
      </div>
      <AdminTargetSessions
        v-if="sessionsOpen" :key="selected.id" :user-id="selected.id" :csrf="csrf" :ui-language="uiLanguage" :translate="translate"
        @step-up="emit('step-up')" @session-changed="emit('session-changed')"
      />
      <fieldset v-if="membership" :disabled="busy || confirmation === 'memberships'">
        <legend>{{ translate('admin.users_memberships') }}</legend>
        <label v-for="role in membership.options" :key="role.id" class="access-check">
          <input v-model="chosen" type="checkbox" :value="role.id" :disabled="!chosen.includes(role.id) && chosen.length >= membership.max_roles">
          {{ role.name }} · {{ translate(`admin.access_state_${role.state}`) }}
        </label>
        <button type="button" :disabled="additions + removals === 0" @click="confirmation = 'memberships'">{{ translate('admin.users_save_memberships') }}</button>
      </fieldset>
      <div v-if="confirmation" class="access-confirm">
        <p>{{ translate(`admin.users_confirm_${confirmation}`) }}</p>
        <p v-if="confirmation === 'memberships'">+{{ additions }} / −{{ removals }}</p>
        <div class="access-actions">
          <button type="button" :disabled="busy" @click="confirm">{{ translate('admin.verify') }}</button>
          <button type="button" :disabled="busy" @click="confirmation = undefined">{{ translate('admin.cancel') }}</button>
        </div>
      </div>
      <div v-if="issue" role="status" class="access-confirm">
        <p>{{ translate('admin.users_invite_delivery') }}</p>
        <code>{{ issue.token }}</code>
        <p>{{ translate('admin.account_expires') }} {{ date(issue.expires_at) }}</p>
        <button type="button" @click="issue = undefined">{{ translate('admin.account_done') }}</button>
      </div>
    </section>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
