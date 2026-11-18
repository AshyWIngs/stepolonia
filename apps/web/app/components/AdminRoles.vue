<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { copyAccessRole, listAccessRoles, roleGrantOptions, saveAccessRole, updateRoleState } from '~/utils/admin-access'
import type { AccessRole, RoleAction, RoleGrant, RoleGrantOption } from '~/utils/admin-access'
import { accessNotice } from '~/utils/admin-access-feedback'

const props = defineProps<{ csrf: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [] }>()
const roles = ref<AccessRole[]>([])
const selected = ref<AccessRole>()
const next = ref<number | null>(null)
const offset = ref(0)
const busy = ref(false)
const error = ref('')
const feedback = ref('')
const heading = ref<HTMLElement>()
const mode = ref<'edit' | 'create' | 'copy'>()
const name = ref('')
const staged = ref<RoleGrant[]>([])
const options = ref<RoleGrantOption[]>([])
const currentOption = ref<number>()
const selectedFields = ref<string[]>([])
const delegateOperations = ref<string[]>([])
const allowDelegation = ref(false)
const confirmCommand = ref<RoleAction>()
const option = computed(() => currentOption.value === undefined ? undefined : options.value[currentOption.value])
const actions = computed<RoleAction[]>(() => {
  switch (selected.value?.state) {
    case 'active': return ['disable']
    case 'disabled': return ['enable', 'archive']
    case 'archived': return ['restore', 'delete']
    default: return []
  }
})
let active = true

watch(currentOption, () => {
  selectedFields.value = []
  delegateOperations.value = []
  allowDelegation.value = false
})

async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  feedback.value = ''
  try { await action() }
  catch (problem: unknown) {
    if (active) error.value = accessNotice(problem, () => emit('step-up'), () => emit('session-changed'))
  } finally { busy.value = false }
}

async function load(page = 0) {
  const result = await listAccessRoles(page)
  if (!active) return
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
  if (active) heading.value?.focus()
}

function startEdit(value: 'edit' | 'create' | 'copy') {
  return run(async () => {
    const available = value === 'copy' ? undefined : await roleGrantOptions()
    if (!active) return
    options.value = available?.options ?? []
    currentOption.value = undefined
    staged.value = value === 'edit' ? selected.value?.grants.slice() ?? [] : []
    name.value = value === 'edit' ? selected.value?.name ?? '' : ''
    mode.value = value
  })
}

function label(grant: RoleGrant | RoleGrantOption) {
  const type = 'type_key' in grant ? grant.type_key : options.value.find(entry => entry.type_id === grant.type_id)?.type_key
  return [grant.operation, type || grant.type_id, grant.object_scope || grant.target_scope, grant.object_ceiling].filter(Boolean).join(' · ')
}

function fieldName(id: string) {
  for (const choice of options.value) {
    const found = choice.fields?.find(field => field.id === id)
    if (found) return found.key
  }
  return id
}

function addGrant() {
  const choice = option.value
  if (!choice || staged.value.length >= 64 || (choice.delegate_operations?.length && !delegateOperations.value.length)) return
  const grant: RoleGrant = { operation: choice.operation, scope: choice.scope, can_delegate: choice.can_delegate && allowDelegation.value }
  if (choice.type_id) grant.type_id = choice.type_id
  if (choice.object_scope) grant.object_scope = choice.object_scope
  if (choice.target_scope) {
    grant.target_scope = choice.target_scope
    grant.object_ceiling = choice.object_ceiling
    grant.may_delegate_content = Boolean(choice.may_delegate_content && allowDelegation.value)
    grant.delegate_operations = delegateOperations.value.slice()
  }
  if (choice.fields && selectedFields.value.length > 0) grant.field_ids = selectedFields.value.slice()
  const duplicate = staged.value.some(previous => previous.operation === grant.operation && previous.type_id === grant.type_id &&
    previous.object_scope === grant.object_scope && previous.target_scope === grant.target_scope)
  if (duplicate) {
    error.value = 'admin.roles_duplicate'
    return
  }
  staged.value.push(grant)
  currentOption.value = undefined
  error.value = ''
}

function save() {
  const action = mode.value
  const role = selected.value
  if (!action || (action !== 'create' && !role)) return
  return run(async () => {
    const saved = action === 'copy' && role
      ? await copyAccessRole(role, name.value, props.csrf)
      : await saveAccessRole(action === 'edit' ? role : undefined, name.value, staged.value, props.csrf)
    if (!active) return
    selected.value = saved
    mode.value = undefined
    await load()
    if (active) feedback.value = 'admin.access_saved'
  })
}

function transition() {
  const role = selected.value
  const action = confirmCommand.value
  if (!role || !action) return
  return run(async () => {
    const updated = await updateRoleState(role, action, props.csrf)
    if (!active) return
    selected.value = updated || undefined
    confirmCommand.value = undefined
    await load(offset.value)
    if (active) feedback.value = 'admin.access_saved'
  })
}

function reload() {
  selected.value = undefined
  mode.value = undefined
  confirmCommand.value = undefined
  return run(() => load())
}

onMounted(() => { void run(() => load()) })
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="access-panel" :aria-busy="busy">
    <h2>{{ translate('admin.roles') }}</h2>
    <p v-if="error" role="alert">{{ translate(error) }}</p>
    <p v-if="feedback" role="status">{{ translate(feedback) }}</p>
    <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
    <div class="access-actions">
      <button type="button" :disabled="busy || Boolean(mode)" @click="startEdit('create')">{{ translate('admin.roles_create') }}</button>
      <button type="button" :disabled="busy" @click="reload">{{ translate('admin.access_reload') }}</button>
    </div>
    <ul class="access-rows">
      <li v-for="role in roles" :key="role.id">
        <button type="button" :disabled="busy || Boolean(mode)" :aria-pressed="role.id === selected?.id" @click="select(role)">{{ role.name }}</button>
        · {{ translate(`admin.access_state_${role.state}`) }}
      </li>
    </ul>
    <div class="access-actions">
      <button v-if="offset" type="button" :disabled="busy || Boolean(mode)" @click="run(() => load(Math.max(0, offset - 20)))">{{ translate('previous') }}</button>
      <button v-if="next !== null" type="button" :disabled="busy || Boolean(mode)" @click="run(() => load(next ?? 0))">{{ translate('next') }}</button>
    </div>
    <section v-if="selected && mode !== 'create'" aria-labelledby="access-role-title">
      <h3 id="access-role-title" ref="heading" tabindex="-1">{{ selected.name }}</h3>
      <p>{{ translate(`admin.access_state_${selected.state}`) }}</p>
      <ul v-if="!mode">
        <li v-for="(grant, index) in selected.grants" :key="index">{{ label(grant) }}</li>
      </ul>
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
      <input id="access-role-name" v-model="name" required maxlength="128" :disabled="busy">
      <template v-if="mode !== 'copy'">
        <h3>{{ translate('admin.roles_grants') }}</h3>
        <ul class="access-rows">
          <li v-for="(grant, index) in staged" :key="index">
            {{ label(grant) }}
            <span v-if="grant.field_ids?.length"> · {{ grant.field_ids.map(fieldName).join(', ') }}</span>
            <span v-if="grant.delegate_operations?.length"> · {{ grant.delegate_operations.join(', ') }}</span>
            <button type="button" :disabled="busy" @click="staged.splice(index, 1)">{{ translate('admin.roles_remove_grant') }}</button>
          </li>
        </ul>
        <label for="access-grant-option">{{ translate('admin.roles_operation') }}</label>
        <select id="access-grant-option" v-model="currentOption" :disabled="busy">
          <option :value="undefined">{{ translate('admin.roles_choose_operation') }}</option>
          <option v-for="(choice, index) in options" :key="index" :value="index">{{ label(choice) }}</option>
        </select>
        <fieldset v-if="option?.fields?.length" :disabled="busy">
          <legend>{{ translate('admin.roles_fields') }}</legend>
          <label v-for="field in option.fields" :key="field.id" class="access-check">
            <input v-model="selectedFields" type="checkbox" :value="field.id"> {{ field.key }}
          </label>
        </fieldset>
        <fieldset v-if="option?.delegate_operations?.length" :disabled="busy">
          <legend>{{ translate('admin.roles_delegated_operations') }}</legend>
          <label v-for="operation in option.delegate_operations" :key="operation" class="access-check">
            <input v-model="delegateOperations" type="checkbox" :value="operation"> {{ operation }}
          </label>
        </fieldset>
        <label v-if="option?.can_delegate || option?.may_delegate_content" class="access-check">
          <input v-model="allowDelegation" type="checkbox" :disabled="busy"> {{ translate('admin.roles_allow_delegation') }}
        </label>
        <button v-if="option" type="button" :disabled="busy || staged.length >= 64" @click="addGrant">{{ translate('admin.roles_add_grant') }}</button>
      </template>
      <div class="access-actions">
        <button type="submit" :disabled="busy">{{ translate('admin.roles_save') }}</button>
        <button type="button" :disabled="busy" @click="mode = undefined">{{ translate('admin.cancel') }}</button>
      </div>
    </form>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
