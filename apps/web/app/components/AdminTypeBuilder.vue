<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { AdminContentError, createCollection, listCollections, listRegistryRenderers, readManagedCollection, reviseCollection } from '~/utils/admin-content'
import type { CollectionChoice, FieldKind, ManagedCollection, RegistryField, RegistryRenderer } from '~/utils/admin-content'

const props = defineProps<{
  csrf: string
  uiLanguage: string
  translate: (key: string) => string
}>()
const emit = defineEmits<{ close: [], 'step-up': [], 'session-changed': [] }>()
const renderers = ref<RegistryRenderer[]>([])
const targets = ref<CollectionChoice[]>([])
const selected = ref('')
const collection = ref<ManagedCollection>()
const fields = ref<RegistryField[]>([])
const baselineKeys = ref<string[]>([])
const initialize = ref(false)
const busy = ref(false)
const dirty = ref(false)
const failure = ref('')
const feedback = ref('')
const conflict = ref(false)
const kinds: FieldKind[] = ['string', 'text', 'markdown', 'boolean', 'integer', 'decimal', 'datetime', 'enum', 'relation']
let active = true

const renderer = computed(() => renderers.value.find(item => item.type_key === selected.value))
const canEdit = computed(() => renderer.value && fields.value.length > 0 &&
  (renderer.value.available ? !collection.value : collection.value?.type_key === renderer.value.type_key))
const hasNewFields = computed(() => fields.value.some(field => !baselineKeys.value.includes(field.key)))

function showError(error: unknown) {
  failure.value = 'admin.unavailable'
  if (!(error instanceof AdminContentError)) return
  if (error.code === 'step_up_required') {
    failure.value = 'admin.step_up_required'
    emit('step-up')
  }
  else if (error.status === 401 || error.code === 'csrf_failed') {
    failure.value = 'admin.changed'
    emit('session-changed')
  }
  else if (error.status === 403) failure.value = 'admin.access_denied'
  else if (error.status === 409) {
    conflict.value = ['version_conflict', 'collection_exists'].includes(error.code ?? '')
    failure.value = conflict.value ? 'admin.registry_conflict' : 'admin.registry_invalid'
  }
  else if (error.status === 400) failure.value = 'admin.registry_invalid'
}

async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  feedback.value = ''
  try { await work() }
  catch (error: unknown) { if (active) showError(error) }
  finally { busy.value = false }
}

function setFields(list: RegistryField[]) {
  fields.value = list.map(field => ({
    key: field.key,
    kind: field.kind,
    scope: field.scope,
    visibility: field.visibility,
    required: field.required,
    target_type_id: field.target_type_id,
    config: field.config,
    display_labels: field.display_labels,
  }))
  baselineKeys.value = list.map(field => field.key)
  initialize.value = false
  dirty.value = false
  conflict.value = false
}

async function openRenderer() {
  const choice = renderer.value
  collection.value = undefined
  setFields([])
  if (!choice) return
  const loaded = choice.available ? undefined : await readManagedCollection(choice.type_key)
  if (!active || selected.value !== choice.type_key) return
  if (loaded && loaded.type_key !== choice.type_key) throw new Error('Collection response does not match the selected type')
  collection.value = loaded
  setFields(loaded?.fields ?? choice.fields)
}

async function load() {
  await run(async () => {
    const [result, types] = await Promise.all([listRegistryRenderers(), listCollections()])
    if (!active) return
    renderers.value = result.items
    targets.value = types.items
    if (!selected.value) selected.value = result.items.find(item => item.available)?.type_key ?? result.items[0]?.type_key ?? ''
    await openRenderer()
  })
}

function addField() {
  fields.value.push({ key: '', kind: 'string', scope: 'localized', visibility: 'private', required: false })
  dirty.value = true
}

function removeField(index: number) {
  fields.value.splice(index, 1)
  dirty.value = true
}

function labelFor(field: RegistryField, part: 'label' | 'help') {
  return field.display_labels?.[props.uiLanguage]?.[part] ?? ''
}

function updateLabel(field: RegistryField, part: 'label' | 'help', event: Event) {
  const control = event.currentTarget
  if (!(control instanceof HTMLInputElement)) return
  const locale = props.uiLanguage
  field.display_labels = { ...field.display_labels, [locale]: { ...field.display_labels?.[locale], [part]: control.value } }
  dirty.value = true
}

function updateCodes(field: RegistryField, event: Event) {
  const control = event.currentTarget
  if (control instanceof HTMLInputElement) {
    field.config = { codes: control.value.split(',').map(code => code.trim()).filter(Boolean) }
    dirty.value = true
  }
}

function changeKind(field: RegistryField) {
  field.config = undefined
  field.target_type_id = undefined
}

async function save() {
  const choice = renderer.value
  if (!choice || !canEdit.value || conflict.value) return
  await run(async () => {
    const wasPresent = collection.value
    const saved = wasPresent
      ? await reviseCollection(wasPresent, fields.value, hasNewFields.value && initialize.value, props.csrf)
      : await createCollection(choice, fields.value, initialize.value, props.csrf)
    if (!active) return
    collection.value = saved
    renderers.value = renderers.value.map(item => item.type_key === saved.type_key ? { ...item, available: false } : item)
    setFields(saved.fields)
    feedback.value = wasPresent ? 'admin.registry_revised' : 'admin.registry_created'
  })
}

onMounted(() => { void load() })
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="builder" :aria-busy="busy">
    <div class="builder-actions">
      <button type="button" class="admin-secondary" :disabled="busy" @click="emit('close')">{{ translate('admin.close') }}</button>
    </div>
    <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <p v-if="feedback" role="status">{{ translate(feedback) }}</p>
    <button v-if="conflict || (failure && !canEdit)" type="button" :disabled="busy" @click="load">{{ translate('admin.registry_reload') }}</button>
    <form v-if="renderer" @submit.prevent="save" @input="dirty = true">
      <label for="registry-renderer">{{ translate('admin.registry_choose') }}</label>
      <select id="registry-renderer" v-model="selected" :disabled="busy || dirty" @change="run(openRenderer)">
        <option v-for="item in renderers" :key="item.type_key" :value="item.type_key">{{ item.type_key }}</option>
      </select>
      <template v-if="canEdit">
        <p v-if="collection">{{ translate('admin.registry_schema') }} {{ collection.schema_no }}</p>
        <fieldset v-for="(field, index) in fields" :key="index" :disabled="busy">
          <legend>{{ field.key || translate('admin.registry_new_field') }}</legend>
          <template v-if="index >= baselineKeys.length">
            <label :for="`registry-key-${index}`">{{ translate('admin.registry_field_key') }}</label>
            <input :id="`registry-key-${index}`" v-model="field.key" required pattern="[a-z][a-z0-9_]{0,63}">
            <label :for="`registry-kind-${index}`">{{ translate('admin.registry_kind') }}</label>
            <select :id="`registry-kind-${index}`" v-model="field.kind" @change="changeKind(field)">
              <option v-for="kind in kinds" :key="kind" :value="kind">{{ kind }}</option>
            </select>
            <label :for="`registry-scope-${index}`">{{ translate('admin.registry_scope') }}</label>
            <select :id="`registry-scope-${index}`" v-model="field.scope">
              <option value="localized">{{ translate('admin.content_translation') }}</option>
              <option value="shared">{{ translate('admin.content_shared') }}</option>
            </select>
            <label :for="`registry-visibility-${index}`">{{ translate('admin.registry_visibility') }}</label>
            <select :id="`registry-visibility-${index}`" v-model="field.visibility">
              <option value="private">{{ translate('admin.registry_private') }}</option>
              <option value="public">{{ translate('admin.registry_public') }}</option>
            </select>
            <template v-if="field.kind === 'enum'">
              <label :for="`registry-codes-${index}`">{{ translate('admin.registry_codes') }}</label>
              <input :id="`registry-codes-${index}`" required :value="field.config?.codes?.join(', ')" @input="updateCodes(field, $event)">
            </template>
            <template v-if="field.kind === 'relation'">
              <label :for="`registry-target-${index}`">{{ translate('admin.registry_target') }}</label>
              <select :id="`registry-target-${index}`" v-model="field.target_type_id" required>
                <option v-for="target in targets" :key="target.type_id" :value="target.type_id">{{ target.type_key }}</option>
              </select>
            </template>
            <button type="button" class="admin-secondary" @click="removeField(index)">{{ translate('admin.registry_remove_field') }}</button>
          </template>
          <p v-else>{{ field.kind }} · {{ field.scope }} · {{ field.visibility }}</p>
          <label :for="`registry-label-${index}`">{{ translate('admin.registry_label') }}</label>
          <input :id="`registry-label-${index}`" :value="labelFor(field, 'label')" maxlength="4096" @input="updateLabel(field, 'label', $event)">
          <label :for="`registry-help-${index}`">{{ translate('admin.registry_help') }}</label>
          <input :id="`registry-help-${index}`" :value="labelFor(field, 'help')" maxlength="4096" @input="updateLabel(field, 'help', $event)">
        </fieldset>
        <button type="button" class="admin-secondary" :disabled="busy || fields.length >= 64" @click="addField">{{ translate('admin.registry_add_field') }}</button>
        <label v-if="!collection || hasNewFields" class="builder-check">
          <input v-model="initialize" type="checkbox" :disabled="busy">
          {{ translate(collection ? 'admin.registry_new_field_access' : 'admin.registry_initial_access') }}
        </label>
        <button type="submit" :disabled="busy || conflict || Boolean(collection && !dirty)">
          {{ translate(collection ? 'admin.registry_save' : 'admin.registry_create') }}
        </button>
      </template>
    </form>
  </section>
</template>

<style scoped>
.builder, .builder form, .builder fieldset {
  display: grid;
  gap: .75rem;
  min-width: 0;
  overflow-wrap: anywhere;
}

.builder fieldset {
  margin: 0;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
}

.builder input, .builder select, .builder button {
  box-sizing: border-box;
  min-height: 44px;
  min-width: 0;
  max-width: 100%;
  padding: .5rem .75rem;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
}

.builder button[type='submit'] {
  background: var(--admin-action);
  color: var(--admin-action-text);
}

.builder :focus-visible {
  outline: 3px solid var(--admin-focus);
  outline-offset: 3px;
}

.builder-check {
  display: flex;
  align-items: center;
  gap: .55rem;
}
</style>
