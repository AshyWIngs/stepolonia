<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { copyAccessRole, listAccessRoles, roleGrantOptions, saveAccessRole, updateRoleState } from '~/utils/admin-access'
import type { AccessRole, RoleAction, RoleGrant, RoleGrantOption } from '~/utils/admin-access'
import { useAccessActions } from '~/utils/admin-access-feedback'
import { collectionName, groupRules, operationName, ruleDomain, ruleKey, ruleScope } from '~/utils/admin-role-rules'

const props = defineProps<{ csrf: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [], 'editing-state': [state: { dirty: boolean, busy: boolean }] }>()
const roles = ref<AccessRole[]>([])
const selected = ref<AccessRole>()
const next = ref<number | null>(null)
const offset = ref(0)
const { active, busy, error, feedback, run } = useAccessActions(() => { emit('step-up') }, () => { emit('session-changed') })
const heading = ref<HTMLElement>()
const mode = ref<'edit' | 'create' | 'copy'>()
const name = ref('')
const nameField = ref<HTMLInputElement>()
const staged = ref<RoleGrant[]>([])
const options = ref<RoleGrantOption[]>([])
const confirmCommand = ref<RoleAction>()
watch([() => Boolean(mode.value), busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const groupedRoles = computed(() => groupRules(roles.value, (role) => {
  const domains = new Set(role.grants.map(grant => ruleDomain(grant.operation)))
  if (domains.size > 1) return 'mixed'
  return domains.values().next().value ?? 'other'
}))
const groupedGrants = computed(() => groupRules(selected.value?.grants ?? [], grant => ruleDomain(grant.operation)))
const incomplete = computed(() => mode.value !== 'copy' && staged.value.some(grant =>
  grant.target_scope && !grant.delegate_operations?.length,
))
const actions = computed<RoleAction[]>(() => {
  switch (selected.value?.state) {
    case 'active': return ['disable']
    case 'disabled': return ['enable', 'archive']
    case 'archived': return ['restore', 'delete']
    default: return []
  }
})

async function load(page = 0) {
  const result = await listAccessRoles(page)
  if (!active.value) return
  offset.value = page
  roles.value = result.items
  next.value = result.next_offset
}

async function select(role: AccessRole) {
  selected.value = role
  mode.value = undefined
  confirmCommand.value = undefined
  error.value = ''
  feedback.value = ''
  await nextTick()
  if (active.value) heading.value?.focus()
}

function startEdit(value: 'edit' | 'create' | 'copy') {
  return run(async () => {
    const available = value === 'copy' ? undefined : await roleGrantOptions()
    if (!active.value) return
    options.value = available?.options ?? []
    staged.value = value === 'edit' ? selected.value?.grants.slice() ?? [] : []
    name.value = value === 'edit' ? selected.value?.name ?? '' : ''
    mode.value = value
  })
    .then(async () => {
      if (!active.value || !mode.value) return
      await nextTick()
      nameField.value?.focus()
    })
}

function label(grant: RoleGrant) {
  const key = grant.type_id && selected.value?.collection_keys?.[grant.type_id]
  const collection = key ? collectionName(key, props.translate) : grant.type_id
  return `${operationName(grant.operation, props.translate)} · ${ruleScope(grant, collection, props.translate)}`
}

function save() {
  const action = mode.value
  const role = selected.value
  if (!action || incomplete.value || (action !== 'create' && !role)) return
  const roleToUpdate = action === 'edit' ? role : undefined
  return run(async () => {
    const saved = action === 'copy' && role
      ? await copyAccessRole(role, name.value, props.csrf)
      : await saveAccessRole(roleToUpdate, name.value, staged.value, props.csrf)
    if (!active.value) return
    selected.value = saved
    mode.value = undefined
    await load()
    if (active.value) feedback.value = 'admin.access_saved'
  })
}

function transition() {
  const role = selected.value
  const action = confirmCommand.value
  if (!role || !action) return
  return run(async () => {
    const updated = await updateRoleState(role, action, props.csrf)
    if (!active.value) return
    selected.value = updated ?? undefined
    confirmCommand.value = undefined
    await load(offset.value)
    if (active.value) feedback.value = 'admin.access_saved'
  })
}

function reload() {
  selected.value = undefined
  mode.value = undefined
  confirmCommand.value = undefined
  return run(() => load())
}

onMounted(() => { void run(() => load()) })
</script>

<template>
  <section class="access-panel" :aria-busy="busy">
    <h2>{{ translate('admin.roles') }}</h2>
    <p v-if="error" role="alert">{{ translate(error) }}</p>
    <p v-if="feedback"><output>{{ translate(feedback) }}</output></p>
    <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
    <div class="access-actions">
      <button type="button" :disabled="busy || Boolean(mode)" @click="startEdit('create')">{{ translate('admin.roles_create') }}</button>
      <button type="button" :disabled="busy" @click="reload">{{ translate('admin.access_reload') }}</button>
    </div>
    <section v-for="[domain, entries] in groupedRoles" :key="domain" class="role-directory">
      <h3>{{ translate(`admin.role_rule_group_${domain}`) }}</h3>
      <ul>
        <li v-for="role in entries" :key="role.id">
          <button type="button" :disabled="busy || Boolean(mode)" :aria-pressed="role.id === selected?.id" @click="select(role)">{{ role.name }}</button>
          <small>{{ translate(`admin.access_state_${role.state}`) }}</small>
        </li>
      </ul>
    </section>
    <div class="access-actions">
      <button v-if="offset" type="button" :disabled="busy || Boolean(mode)" @click="run(() => load(Math.max(0, offset - 20)))">{{ translate('previous') }}</button>
      <button v-if="next !== null" type="button" :disabled="busy || Boolean(mode)" @click="run(() => load(next ?? 0))">{{ translate('next') }}</button>
    </div>
    <section v-if="selected && mode !== 'create'" aria-labelledby="access-role-title">
      <h3 id="access-role-title" ref="heading" tabindex="-1">{{ selected.name }}</h3>
      <p>{{ translate(`admin.access_state_${selected.state}`) }}</p>
      <div v-if="!mode" class="role-summary">
        <section v-for="[domain, grants] in groupedGrants" :key="domain">
          <h4>{{ translate(`admin.role_rule_group_${domain}`) }}</h4>
          <ul>
            <li v-for="grant in grants" :key="ruleKey(grant)">{{ label(grant) }}</li>
          </ul>
        </section>
      </div>
      <div v-if="!mode && !confirmCommand" class="access-actions">
        <button type="button" :disabled="busy" @click="startEdit('edit')">{{ translate('admin.roles_edit') }}</button>
        <button type="button" :disabled="busy" @click="startEdit('copy')">{{ translate('admin.roles_copy') }}</button>
        <button v-for="action in actions" :key="action" type="button" :disabled="busy" @click="confirmCommand = action">
          {{ translate(`admin.access_${action}`) }}
        </button>
      </div>
      <div v-if="confirmCommand" class="access-confirm">
        <p>{{ translate(`admin.roles_confirm_${confirmCommand}`) }}</p>
        <div class="access-actions">
          <button type="button" :disabled="busy" @click="transition">{{ translate('admin.verify') }}</button>
          <button type="button" :disabled="busy" @click="confirmCommand = undefined">{{ translate('admin.cancel') }}</button>
        </div>
      </div>
    </section>
    <form v-if="mode" @submit.prevent="save">
      <label for="access-role-name">{{ translate('admin.roles_name') }}</label>
      <input id="access-role-name" ref="nameField" v-model="name" required maxlength="128" :disabled="busy">
      <div class="access-actions role-edit-actions">
        <button type="submit" :disabled="busy || incomplete">{{ translate('admin.roles_save') }}</button>
        <button type="button" :disabled="busy" @click="mode = undefined">{{ translate('admin.cancel') }}</button>
        <output v-if="incomplete">{{ translate('admin.role_rule_choose_delegated_operation') }}</output>
      </div>
      <template v-if="mode !== 'copy'">
        <h3>{{ translate('admin.roles_grants') }}</h3>
        <AdminRoleRules
          v-model="staged" :options="options" :collection-keys="mode === 'edit' ? selected?.collection_keys : undefined"
          :original="mode === 'edit' ? selected?.grants ?? [] : []" :busy="busy" :translate="translate"
        />
      </template>
    </form>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';

.role-directory h3, .role-summary h4 { margin-block: .25rem; }
.role-directory ul { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr)); gap: .5rem; list-style: none; margin: 0; padding: 0; }
.role-directory li { display: grid; gap: .25rem; align-content: start; border: 1px solid var(--admin-border); border-radius: .4rem; padding: .5rem; min-inline-size: 0; }
.role-directory button { text-align: start; }
.role-directory [aria-pressed="true"] { color: var(--admin-action-text); background: var(--admin-action); }
.role-directory small { color: var(--admin-muted); }
.role-summary { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 14rem), 1fr)); gap: .5rem; }
.role-summary ul { margin-block: 0 .5rem; padding-inline-start: 1.25rem; }
.role-edit-actions { position: sticky; inset-block-start: 0; z-index: 1; padding-block: .5rem; background: var(--admin-card); }
.role-edit-actions output { flex-basis: 100%; margin: 0; }
</style>
