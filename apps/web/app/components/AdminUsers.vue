<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { changeUserState, createAccessUser, issueInvitation, listAccessUsers, readMembership, replaceMembership } from '~/utils/admin-access'
import type { AccessUser, Membership, MembershipSet } from '~/utils/admin-access'
import { useAccessActions } from '~/utils/admin-access-feedback'
import { AdminContentError } from '~/utils/admin-request'

type UserAction = 'activate' | 'disable' | 'invite'

const membershipLabels: Record<Membership['domain'], string> = {
  content: 'admin.content',
  structure: 'admin.workspace_structure',
  settings: 'admin.settings',
  access: 'admin.access',
  mixed: 'admin.users_roles_mixed',
  other: 'admin.users_roles_other',
}

const props = defineProps<{ csrf: string, uiLanguage: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [], 'editing-state': [state: { dirty: boolean, busy: boolean }] }>()
const users = ref<AccessUser[]>([])
const selected = ref<AccessUser>()
const membership = ref<MembershipSet>()
const membershipConflict = ref(false)
const membershipHeading = ref<HTMLElement>()
const membershipEntry = ref<HTMLButtonElement>()
const chosen = ref<string[]>([])
const nextOffset = ref<number | null>(null)
const pageOffset = ref(0)
const sessionsOpen = ref(false)
const sessionsBusy = ref(false)
const name = ref('')
const issue = ref<{ token: string, expires_at: string }>()
const panel = ref<HTMLElement>()
const confirmation = ref<UserAction>()
const { active, busy, error, feedback, run } = useAccessActions(() => { emit('step-up') }, () => { emit('session-changed') })
const membershipDirty = computed(() => {
  const current = membership.value?.role_ids
  return Boolean(current && (current.length !== chosen.value.length || current.some(id => !chosen.value.includes(id))))
})
const membershipGroups = computed(() => {
  const options = membership.value?.options ?? []
  return Object.entries(membershipLabels).flatMap(([domain, label]) => {
    const roles = options.filter(role => (role.domain ?? 'other') === domain)
    return roles.length ? [{ domain, label, roles }] : []
  })
})
const usersBusy = computed(() => busy.value || sessionsBusy.value)
const usersLocked = computed(() => usersBusy.value || membershipDirty.value || membershipConflict.value)
watch([() => Boolean(name.value || membershipDirty.value || issue.value), usersBusy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})

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
  if (membershipDirty.value || membershipConflict.value) return
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

function mergeMembershipChoices(before: MembershipSet, latest: MembershipSet, pending: string[]) {
  const kept = latest.role_ids.filter(id => !before.role_ids.includes(id) || pending.includes(id))
  const additions = pending.filter(id => !before.role_ids.includes(id) && !kept.includes(id))
  const eligible = additions.filter(id => latest.options.some(role => role.id === id))
  if (eligible.length !== additions.length) feedback.value = 'admin.users_roles_unavailable'
  return [...kept, ...eligible]
}

async function loadMembership(user: AccessUser, before?: MembershipSet) {
  const found = await readMembership(user)
  if (!active.value || selected.value?.id !== user.id) return
  chosen.value = before ? mergeMembershipChoices(before, found, chosen.value) : found.role_ids.slice()
  membership.value = found
  membershipConflict.value = false
  await nextTick()
  if (active.value) membershipHeading.value?.focus()
}

function saveMembership() {
  const current = membership.value
  if (!current || !membershipDirty.value || membershipConflict.value || chosen.value.length > current.max_roles) return
  return run(async () => {
    const updated = await replaceMembership(current, chosen.value, props.csrf).catch((problem: unknown) => {
      if (active.value && problem instanceof AdminContentError && problem.code === 'version_conflict') membershipConflict.value = true
      throw problem
    })
    if (!active.value) return
    updateUser(updated)
    feedback.value = 'admin.access_saved'
    try {
      await loadMembership(updated)
    } catch (problem: unknown) {
      if (active.value) membership.value = undefined
      throw problem
    }
  })
}

async function resetMembership() {
  if (!membership.value) return
  if (!membershipConflict.value) chosen.value = membership.value.role_ids.slice()
  else membership.value = undefined
  membershipConflict.value = false
  error.value = ''
  feedback.value = ''
  await nextTick()
  if (active.value) (membership.value ? membershipHeading.value : membershipEntry.value)?.focus()
}

function saveUser(user: AccessUser, action: UserAction) {
  return run(async () => {
    await applyUserCommand(user, action)
    if (!active.value) return
    confirmation.value = undefined
    feedback.value = 'admin.access_saved'
  })
}

function applyUserCommand(user: AccessUser, action: UserAction) {
  switch (action) {
    case 'invite': return issueInvitation(user, props.csrf).then(result => {
      if (!active.value) return
      updateUser(result.user)
      issue.value = { token: result.token, expires_at: result.expires_at }
    })
    default: return changeUserState(user, action, props.csrf).then(updated => {
      if (!active.value) return
      updateUser(updated)
      membership.value = undefined
      sessionsOpen.value = false
    })
  }
}

function openMembership() {
  const user = selected.value
  const before = membershipConflict.value ? membership.value : undefined
  if (user) return run(() => loadMembership(user, before))
}

function confirm() {
  const user = selected.value
  const action = confirmation.value
  if (!user || !action) return
  return saveUser(user, action)
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
  <section class="access-panel" :aria-busy="usersBusy">
    <h2>{{ translate('admin.users') }}</h2>
    <p v-if="error" role="alert">{{ translate(error) }}</p>
    <p v-if="feedback"><output>{{ translate(feedback) }}</output></p>
    <form @submit.prevent="create">
      <label for="access-new-login">{{ translate('admin.login') }}</label>
      <input id="access-new-login" v-model="name" :disabled="usersLocked" required maxlength="256">
      <button type="submit" :disabled="usersLocked">{{ translate('admin.users_create') }}</button>
    </form>
    <output v-if="usersBusy">{{ translate('admin.working') }}</output>
    <ul class="access-rows">
      <li v-for="user in users" :key="user.id">
        <button type="button" :disabled="usersLocked" :aria-pressed="selected?.id === user.id" @click="select(user)">{{ user.login }}</button>
        · {{ translate(`admin.access_state_${user.state}`) }}
      </li>
    </ul>
    <div class="access-actions">
      <button v-if="pageOffset" type="button" :disabled="usersLocked" @click="load(Math.max(0, pageOffset - 20))">{{ translate('previous') }}</button>
      <button v-if="nextOffset !== null" type="button" :disabled="usersLocked" @click="load(nextOffset)">{{ translate('next') }}</button>
      <button type="button" :disabled="usersLocked" @click="reset">{{ translate('admin.access_reload') }}</button>
    </div>
    <section v-if="selected" aria-labelledby="access-target-account">
      <h3 id="access-target-account" ref="panel" tabindex="-1">{{ selected.login }}</h3>
      <p>{{ translate(`admin.access_state_${selected.state}`) }}</p>
      <div class="access-actions">
        <button v-if="selected.state === 'pending'" type="button" :disabled="usersBusy" @click="confirmation = 'invite'">{{ translate('admin.users_issue_invite') }}</button>
        <template v-else>
          <button v-if="!membership" ref="membershipEntry" type="button" :disabled="usersBusy" @click="openMembership">{{ translate('admin.users_view_roles') }}</button>
          <button v-if="selected.state === 'active'" type="button" :disabled="usersBusy" :aria-expanded="sessionsOpen" @click="sessionsOpen = !sessionsOpen">{{ translate('admin.account_sessions') }}</button>
          <button type="button" :disabled="usersLocked" @click="confirmation = selected.state === 'active' ? 'disable' : 'activate'">
            {{ translate(selected.state === 'active' ? 'admin.access_disable' : 'admin.users_activate') }}
          </button>
        </template>
      </div>
      <AdminTargetSessions
        v-if="sessionsOpen" :key="selected.id" :user-id="selected.id" :csrf="csrf" :ui-language="uiLanguage" :translate="translate"
        @step-up="emit('step-up')" @session-changed="emit('session-changed')" @busy="sessionsBusy = $event"
      />
      <form v-if="membership" @submit.prevent="saveMembership">
        <fieldset class="membership-editor" :disabled="usersBusy || Boolean(confirmation)">
          <legend ref="membershipHeading" tabindex="-1">{{ translate('admin.users_memberships') }} · <bdi>{{ selected.login }}</bdi></legend>
          <p v-if="!membershipGroups.length">{{ translate('admin.users_roles_none') }}</p>
          <div v-else class="membership-groups">
            <section v-for="group in membershipGroups" :key="group.domain" class="membership-group">
              <h4 :id="`membership-domain-${group.domain}`">{{ translate(group.label) }}</h4>
              <table :aria-labelledby="`membership-domain-${group.domain}`">
                <thead>
                  <tr>
                    <th scope="col">{{ translate('admin.roles_name') }}</th>
                    <th scope="col">{{ translate('admin.users_roles_assigned') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="role in group.roles" :key="role.id">
                    <th scope="row">
                      <bdi :id="`membership-role-${role.id}`">{{ role.name }}</bdi>
                      <small>{{ translate(`admin.access_state_${role.state}`) }}</small>
                    </th>
                    <td>
                      <label class="access-check">
                        <input
                          v-model="chosen" type="checkbox" :value="role.id" :aria-labelledby="`membership-role-${role.id}`"
                          :disabled="!chosen.includes(role.id) && chosen.length >= membership.max_roles"
                        >
                      </label>
                    </td>
                  </tr>
                </tbody>
              </table>
            </section>
          </div>
          <p v-if="chosen.length >= membership.max_roles">{{ translate('admin.users_roles_limit') }}</p>
          <div class="access-actions membership-actions">
            <button type="submit" :disabled="!membershipDirty || membershipConflict || chosen.length > membership.max_roles">
              {{ translate('admin.users_apply_roles') }}
            </button>
            <button v-if="membershipConflict" type="button" @click="openMembership">{{ translate('admin.access_reload') }}</button>
            <button v-if="membershipDirty || membershipConflict" type="button" @click="resetMembership">{{ translate('admin.cancel') }}</button>
          </div>
        </fieldset>
      </form>
      <div v-if="confirmation" class="access-confirm">
        <p>{{ translate(`admin.users_confirm_${confirmation}`) }}</p>
        <div class="access-actions">
          <button type="button" :disabled="usersBusy" @click="confirm">{{ translate('admin.verify') }}</button>
          <button type="button" :disabled="usersBusy" @click="confirmation = undefined">{{ translate('admin.cancel') }}</button>
        </div>
      </div>
      <div v-if="issue" class="access-confirm">
        <p><output>{{ translate('admin.users_invite_delivery') }}</output></p>
        <code>{{ issue.token }}</code>
        <p>{{ translate('admin.account_expires') }} {{ date(issue.expires_at) }}</p>
        <button type="button" @click="issue = undefined">{{ translate('admin.account_done') }}</button>
      </div>
    </section>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';

.membership-groups {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr));
  gap: 1rem;
}

.membership-group { min-inline-size: 0; }
.membership-group h4 { margin-block: 0 .5rem; }

.membership-group table {
  border-collapse: collapse;
  inline-size: 100%;
  text-align: start;
}

.membership-group :is(th, td) {
  border-block-end: 1px solid var(--admin-border);
  padding-inline: .25rem;
  text-align: start;
}

.membership-group th[scope='row'] { overflow-wrap: anywhere; }
.membership-group :is(th, td):last-child { inline-size: 1%; text-align: center; white-space: nowrap; }
.membership-group .access-check { justify-content: center; min-inline-size: 44px; }
.membership-group .access-check input { accent-color: var(--admin-action); }

.membership-group small { display: block; color: var(--admin-muted); font-weight: normal; }

.membership-actions { margin-block-start: .75rem; }
</style>
