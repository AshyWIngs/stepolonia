<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { accessRoleChanges, changeAccessRole, copyAccessRole, createAccessRole, listAccessRoles, readAccessRole, roleDisplayName, roleGrantOptions, updateRoleState } from '~/utils/admin-access'
import type { AccessRole, RoleAction, RoleGrant, RoleGrantOption } from '~/utils/admin-access'
import { useAccessActions } from '~/utils/admin-access-feedback'
import { collectionName, groupRules, operationName, ruleDomain, ruleKey, ruleScope } from '~/utils/admin-role-rules'

type RoleEditorMode = 'edit' | 'create' | 'copy'

const props = defineProps<{ csrf: string, uiLanguage: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [], 'permission-texts': [], 'editing-state': [state: { dirty: boolean, busy: boolean }] }>()
const roles = ref<AccessRole[]>([])
const selected = ref<AccessRole>()
const next = ref<number | null>(null)
const offset = ref(0)
const { active, busy, error, feedback, run } = useAccessActions(() => { emit('step-up') }, () => { emit('session-changed') })
const heading = ref<HTMLElement>()
const mode = ref<RoleEditorMode>()
const name = ref('')
const nameField = ref<HTMLInputElement>()
const staged = ref<RoleGrant[]>([])
const options = ref<RoleGrantOption[]>([])
const confirmCommand = ref<RoleAction>()
const namesOpen = ref(false)
const namesState = ref({ dirty: false, busy: false })
const controlsLocked = computed(() => busy.value || Boolean(mode.value) || namesOpen.value)
watch([() => Boolean(mode.value) || namesState.value.dirty, () => busy.value || namesState.value.busy], ([edited, saving]) => {
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
const changes = computed(() => selected.value ? accessRoleChanges(selected.value, name.value, staged.value) : undefined)
const unchanged = computed(() => mode.value === 'edit' && changes.value &&
  changes.value.name === undefined && !changes.value.upsert && !changes.value.remove)
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

function updateDisplayNames(updated: AccessRole) {
  selected.value = updated
  roles.value = roles.value.map(role => role.id === updated.id ? updated : role)
}

async function closeNames() {
  namesOpen.value = false
  namesState.value = { dirty: false, busy: false }
  await nextTick()
  heading.value?.focus()
}

function startEdit(value: RoleEditorMode) {
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
  if (!action || incomplete.value || unchanged.value || (action !== 'create' && !role)) return
  const change = changes.value
  return run(async () => {
    const saved = await saveRoleCommand(action, role, change)
    if (!active.value) return
    selected.value = saved
    mode.value = undefined
    feedback.value = 'admin.access_saved'
    await nextTick()
    heading.value?.focus()
    try {
      await load()
    } catch {
      if (active.value) error.value = 'admin.unavailable'
    }
  })
}

function reloadEditedRole() {
  const current = selected.value
  if (!current) return
  return run(async () => {
    const role = await readAccessRole(current)
    if (active.value) await select(role)
  })
}

async function cancelEdit() {
  mode.value = undefined
  error.value = ''
  await nextTick()
  heading.value?.focus()
}

function saveRoleCommand(action: RoleEditorMode, role: AccessRole | undefined, change: typeof changes.value) {
  if (action === 'edit' && role && change) return changeAccessRole(role, change, props.csrf)
  if (action === 'copy' && role) return copyAccessRole(role, name.value, props.csrf)
  return createAccessRole(name.value, staged.value, props.csrf)
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
    <p v-if="error && !mode" role="alert">{{ translate(error) }}</p>
    <p v-if="feedback"><output>{{ translate(feedback) }}</output></p>
    <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
    <div class="access-actions">
      <button type="button" :disabled="controlsLocked" @click="startEdit('create')">{{ translate('admin.roles_create') }}</button>
      <button type="button" :disabled="busy || namesOpen" @click="reload">{{ translate('admin.access_reload') }}</button>
      <button type="button" :disabled="busy || namesState.busy" @click="emit('permission-texts')">{{ translate('admin.role_permission_texts') }}</button>
    </div>
    <section v-for="[domain, entries] in groupedRoles" :key="domain" class="role-directory">
      <h3>{{ translate(`admin.role_rule_group_${domain}`) }}</h3>
      <ul>
        <li v-for="role in entries" :key="role.id">
          <button type="button" :disabled="controlsLocked" :aria-pressed="role.id === selected?.id" @click="select(role)">
            <bdi>{{ roleDisplayName(role, uiLanguage, roles) }}</bdi>
          </button>
          <small>{{ translate(`admin.access_state_${role.state}`) }}</small>
        </li>
      </ul>
    </section>
    <div class="access-actions">
      <button v-if="offset" type="button" :disabled="controlsLocked" @click="run(() => load(Math.max(0, offset - 20)))">{{ translate('previous') }}</button>
      <button v-if="next !== null" type="button" :disabled="controlsLocked" @click="run(() => load(next ?? 0))">{{ translate('next') }}</button>
    </div>
    <section v-if="selected && mode !== 'create'" aria-labelledby="access-role-title">
      <h3 id="access-role-title" ref="heading" tabindex="-1"><bdi>{{ roleDisplayName(selected, uiLanguage) }}</bdi></h3>
      <p>{{ translate(`admin.access_state_${selected.state}`) }}</p>
      <div v-if="!mode" class="role-summary">
        <section v-for="[domain, grants] in groupedGrants" :key="domain">
          <h4>{{ translate(`admin.role_rule_group_${domain}`) }}</h4>
          <ul>
            <li v-for="grant in grants" :key="ruleKey(grant)">{{ label(grant) }}</li>
          </ul>
        </section>
      </div>
      <div v-if="!mode && !confirmCommand && !namesOpen" class="access-actions">
        <button type="button" :disabled="busy" @click="startEdit('edit')">{{ translate('admin.roles_edit') }}</button>
        <button type="button" :disabled="busy" @click="namesOpen = true">{{ translate('admin.role_names_title') }}</button>
        <button type="button" :disabled="busy" @click="startEdit('copy')">{{ translate('admin.roles_copy') }}</button>
        <button v-for="action in actions" :key="action" type="button" :disabled="busy" @click="confirmCommand = action">
          {{ translate(`admin.access_${action}`) }}
        </button>
      </div>
      <AdminRoleNames
        v-if="namesOpen" :role="selected" :csrf="csrf" :ui-language="uiLanguage" :translate="translate"
        @saved="updateDisplayNames" @close="closeNames" @editing-state="namesState = $event"
        @step-up="emit('step-up')" @session-changed="emit('session-changed')"
      />
      <div v-if="confirmCommand" class="access-confirm">
        <p>{{ translate(`admin.roles_confirm_${confirmCommand}`) }}</p>
        <div class="access-actions">
          <button type="button" :disabled="busy" @click="transition">{{ translate('admin.verify') }}</button>
          <button type="button" :disabled="busy" @click="confirmCommand = undefined">{{ translate('admin.cancel') }}</button>
        </div>
      </div>
    </section>
    <form v-if="mode" class="admin-domain" @submit.prevent="save">
      <label for="access-role-name">{{ translate('admin.roles_name') }}</label>
      <input id="access-role-name" ref="nameField" v-model="name" required maxlength="128" :disabled="busy">
      <div class="role-edit-actions">
        <AdminDomainHeader
          :title="mode === 'edit' ? translate('admin.roles_grants') : undefined"
          :save-label="mode === 'edit' ? translate('admin.roles_save') : undefined"
          :disabled="busy || incomplete || unchanged || error === 'admin.access_version_conflict'"
        >
          <template #actions>
            <button v-if="mode !== 'edit'" type="submit" :disabled="busy || incomplete">{{ translate('admin.roles_save') }}</button>
            <button v-if="mode === 'edit' && error === 'admin.access_version_conflict'" type="button" :disabled="busy" @click="reloadEditedRole">
              {{ translate('admin.reload_settings') }}
            </button>
            <button type="button" :disabled="busy" @click="cancelEdit">{{ translate('admin.cancel') }}</button>
          </template>
        </AdminDomainHeader>
        <output v-if="incomplete">{{ translate('admin.role_rule_choose_delegated_operation') }}</output>
        <p v-if="error" role="alert">{{ translate(error) }}</p>
      </div>
      <template v-if="mode !== 'copy'">
        <h3 v-if="mode === 'create'">{{ translate('admin.roles_grants') }}</h3>
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
.role-edit-actions output { margin: 0; }
</style>
