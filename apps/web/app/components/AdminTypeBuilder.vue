<script setup lang="ts">
import type { components } from '@stepolonia/sdk'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import '~/features/content-registry/registry-editor.css'
import RegistryFieldRow from '~/features/content-registry/RegistryFieldRow.vue'
import RegistryIconEditor from '~/features/content-registry/RegistryIconEditor.vue'
import { useRegistryFieldTranslations } from '~/features/content-registry/useRegistryFieldTranslations'
import { useRegistryFields } from '~/features/content-registry/useRegistryFields'
import { useRegistryIcon } from '~/features/content-registry/useRegistryIcon'
import { createCollection, listCollections, readManagedCollection, setCollectionAvailability } from '~/utils/admin-content'
import type { CollectionChoice, ManagedCollection, RegistryField, RegistryRenderer } from '~/utils/admin-content'
import { AdminContentError } from '~/utils/admin-request'
import { operationName } from '~/utils/admin-role-rules'
import { suggestSlug, type SlugRule } from '~/utils/slug-suggestion'

const props = defineProps<{
  csrf: string
  uiLanguage: string
  languages: components['schemas']['Language'][]
  collectionKey: string
  collectionLabel: (entry: { type_key: string, display_name?: string }) => string
  renderers: RegistryRenderer[]
  slugRules: SlugRule[]
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  'saved': [collection: ManagedCollection]
  'step-up': []
  'session-changed': []
  'editing-state': [state: { dirty: boolean, busy: boolean }]
}>()
const targets = ref<CollectionChoice[]>([])
const selected = ref('')
const newName = ref('')
const iconEditor = ref<InstanceType<typeof RegistryIconEditor>>()
const availabilityDialog = ref<HTMLDialogElement>()
let creationSuffix: string | undefined
const collection = ref<ManagedCollection>()
const collectionChooser = ref<HTMLSelectElement>()
const collectionNameInput = ref<HTMLInputElement>()
const form = ref<HTMLFormElement>()
const addButton = ref<HTMLButtonElement>()
const busy = ref(false)
const failure = ref('')
const feedback = ref('')
const fieldStatus = ref<HTMLOutputElement>()
const conflict = ref(false)
const limited = ref(false)
let retryTimer: ReturnType<typeof setTimeout> | undefined
let active = true

const fields = useRegistryFields()
const { rows, initialize, hasNewFields, visibleRows, dirty: fieldsDirty } = fields
const icon = useRegistryIcon(collection)
const {
  draft: iconSvg, invalid: iconInvalid, conflict: iconConflict, failure: iconFailure, saved: iconSaved,
  changed: changedIcon, preview: previewIcon, previewUnavailable,
} = icon

const available = computed(() => props.renderers.filter(item => item.available))
const renderer = computed(() => available.value.find(item => item.type_key === selected.value))
const isCustom = computed(() => !props.collectionKey && !renderer.value?.type_key)
const canEdit = computed(() => rows.value.length > 0 && (collection.value
  ? collection.value.enabled
  : !props.collectionKey && Boolean(renderer.value)))
const dirty = computed(() => Boolean(newName.value.trim()) || changedIcon.value || fieldsDirty.value)
watch([dirty, busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const {
  translationLocale, translationLanguages, translationLanguage, loadContentLanguages,
  fieldLabel, fieldEditorText, updateLabel,
} = useRegistryFieldTranslations({
  uiLanguage: () => props.uiLanguage,
  languages: () => props.languages,
  fields: () => collection.value?.fields ?? renderer.value?.fields ?? [],
  translate: key => props.translate(key),
  sessionChanged: () => emit('session-changed'),
})

function markInvalidIcon() {
  iconInvalid.value = true
  void nextTick(() => iconEditor.value?.focusInput())
}

function showError(error: unknown, forIcon = false) {
  const message = forIcon ? iconFailure : failure
  if (!(error instanceof AdminContentError)) {
    message.value = 'admin.unavailable'
    return
  }
  if (error.code === 'invalid_collection_icon') {
    markInvalidIcon()
  }
  else if (error.status === 429) {
    message.value = 'admin.limited'
    if (error.retryAfter) {
      limited.value = true
      clearTimeout(retryTimer)
      retryTimer = setTimeout(() => { limited.value = false }, Math.min(error.retryAfter, 2147483) * 1000)
    }
  }
  else if (error.code === 'step_up_required') emit('step-up')
  else if (error.status === 401 || error.code === 'csrf_failed') {
    message.value = 'admin.changed'
    emit('session-changed')
  }
  else if (error.status === 403) message.value = 'admin.access_denied'
  else if (error.status === 409) {
    const state = forIcon ? iconConflict : conflict
    state.value = isRegistryConflict(error.code)
    message.value = registryConflictMessage(state.value)
  }
  else if (error.status === 400) message.value = 'admin.registry_invalid'
  else message.value = 'admin.unavailable'
}

function isRegistryConflict(code?: string) {
  return code === 'version_conflict' || code === 'collection_exists'
}

function registryConflictMessage(conflicted: boolean) {
  return conflicted ? 'admin.registry_conflict' : 'admin.registry_invalid'
}

async function run(work: () => Promise<void>, forIcon = false) {
  if (busy.value) return
  busy.value = true
  if (forIcon) icon.clearFeedback()
  else {
    failure.value = ''
    feedback.value = ''
  }
  try { await work() }
  catch (error: unknown) { if (active) showError(error, forIcon) }
  finally { busy.value = false }
}

function setFields(list: RegistryField[], keepDetails = false) {
  fields.replace(list, keepDetails)
  conflict.value = false
}

async function openRenderer() {
  collection.value = undefined
  setFields([])
  const key = props.collectionKey
  const loaded = key ? await readManagedCollection(key) : undefined
  if (!active || key !== props.collectionKey) return
  if (loaded && loaded.type_key !== key) throw new Error('Collection response does not match the selected type')
  collection.value = loaded
  icon.reset(loaded?.icon_svg ?? '')
  setFields(loaded?.fields ?? renderer.value?.fields ?? [])
}

async function load() {
  await run(async () => {
    await openRenderer()
    const types = await listCollections()
    if (active) targets.value = types.items
  })
}

async function discardChanges() {
  const choice = renderer.value
  if ((!collection.value && !choice) || busy.value) return
  newName.value = ''
  icon.discard()
  setFields(collection.value?.fields ?? choice?.fields ?? [])
  feedback.value = ''
  failure.value = ''
  await nextTick()
  const focusTarget = collectionChooser.value ?? collectionNameInput.value ?? addButton.value
  focusTarget?.focus()
}

async function addField() {
  const id = fields.add()
  await nextTick()
  const key = form.value?.querySelector<HTMLInputElement>(`#registry-key-${id}`)
  if (key) {
    openFieldDetails(key)
    key.focus()
  }
}

async function removeField(id: number) {
  fields.remove(id, Boolean(collection.value))
  await nextTick()
  addButton.value?.focus()
}

function openFieldDetails(control: EventTarget | null) {
  if (!(control instanceof HTMLElement)) return
  const details = control.closest('details')
  if (details) details.open = true
}

async function save() {
  const wasPresent = collection.value
  if (!canEdit.value || conflict.value || limited.value || (wasPresent ? !fieldsDirty.value : iconInvalid.value)) return
  if (!wasPresent && changedIcon.value && !icon.hasValidViewBox()) {
    failure.value = ''
    feedback.value = ''
    markInvalidIcon()
    return
  }
  await run(async () => {
    const saved = wasPresent ? await fields.saveChanges(wasPresent, props.csrf)
      : await saveNewCollection(rows.value.map(row => row.field))
    if (!active || !saved) return
    collection.value = icon.acceptFieldResult(saved, wasPresent)
    newName.value = ''
    setFields(saved.fields, true)
    feedback.value = wasPresent ? 'admin.registry_revised' : 'admin.registry_created'
    emit('saved', saved)
  })
  if (active && feedback.value) await nextTick(() => fieldStatus.value?.focus())
}

async function saveIcon() {
  const current = collection.value
  if (!current || !changedIcon.value || busy.value || limited.value || iconConflict.value || iconInvalid.value) return
  if (!icon.hasValidViewBox()) {
    markInvalidIcon()
    return
  }
  await run(async () => {
    const saved = await icon.save(current, props.csrf)
    if (!active) return
    collection.value = saved
    icon.acceptSaved(saved)
    emit('saved', saved)
  }, true)
  if (active && iconSaved.value) await nextTick(() => iconEditor.value?.focusStatus())
}

async function reloadIcon() {
  const current = collection.value
  if (!current || busy.value) return
  await run(async () => {
    const reloaded = await icon.reload(current)
    if (!active) return
    collection.value = reloaded
    iconConflict.value = false
    emit('saved', reloaded)
  }, true)
  if (active && !iconConflict.value) await nextTick(() => iconEditor.value?.focusInput())
}

function saveNewCollection(fields: RegistryField[]) {
  const choice = renderer.value
  if (!choice) return
  if (choice.type_key) return createCollection(choice, fields, initialize.value, props.csrf, { icon_svg: iconSvg.value })
  const name = newName.value.trim()
  if (!name) return
  creationSuffix ??= crypto.randomUUID().replaceAll('-', '').slice(0, 12)
  const base = suggestSlug(name, props.uiLanguage, props.slugRules, 51 - creationSuffix.length - 1)
  const slug = `${base}-${creationSuffix}`
  const key = `c_${slug.replaceAll('-', '_')}`
  return createCollection({ ...choice, route_namespace: `collection-${key.replaceAll('_', '-')}` }, fields, initialize.value, props.csrf,
    { type_key: key, display_name: name, icon_svg: iconSvg.value })
}

async function changeAvailability() {
  const current = collection.value
  if (!current || busy.value || dirty.value || conflict.value) return
  availabilityDialog.value?.close()
  await run(async () => {
    const saved = await setCollectionAvailability(current, !current.enabled, props.csrf)
    if (!active) return
    collection.value = saved
    feedback.value = saved.enabled ? 'admin.registry_enabled_notice' : 'admin.registry_disabled_notice'
    emit('saved', saved)
  })
}

onMounted(() => {
  void load()
  void loadContentLanguages()
})
onBeforeUnmount(() => {
  active = false
  clearTimeout(retryTimer)
})
</script>

<template>
  <section class="builder registry-builder" :aria-busy="busy">
    <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
    <p v-if="failure && !canEdit" role="alert">{{ translate(failure) }}</p>
    <p v-if="feedback && !canEdit"><output>{{ translate(feedback) }}</output></p>
    <div v-if="canEdit" class="builder-selection">
      <div v-if="!collection && available.length > 1" class="builder-control">
        <label for="registry-renderer">{{ translate('admin.registry_template') }}</label>
        <select id="registry-renderer" ref="collectionChooser" v-model="selected" :disabled="busy || dirty" @change="run(openRenderer)">
          <option v-for="item in available" :key="item.route_namespace" :value="item.type_key">
            {{ item.type_key ? collectionLabel(item) : translate('admin.registry_blank_template') }}
          </option>
        </select>
      </div>
      <div v-if="canEdit && translationLanguage" class="builder-control">
        <label for="registry-translation-language">{{ translate('admin.registry_translation_language') }}</label>
        <select id="registry-translation-language" v-model="translationLocale" :disabled="busy">
          <option v-for="language in translationLanguages" :key="language.code" :value="language.code" :lang="language.code" :dir="language.direction">
            {{ language.native_name }}
          </option>
        </select>
      </div>
      <button v-if="dirty && !conflict" type="button" class="admin-secondary" :disabled="busy" @click="discardChanges">
        {{ translate('admin.discard_changes') }}
      </button>
    </div>
    <template v-if="collection">
      <p v-if="!collection.enabled">{{ translate('admin.registry_disabled_notice') }}</p>
      <button
        type="button" class="admin-secondary" :disabled="busy || dirty || conflict"
        @click="availabilityDialog?.showModal()"
      >{{ translate(collection.enabled ? 'admin.registry_disable' : 'admin.registry_enable') }}</button>
      <dialog ref="availabilityDialog" class="admin-dialog" aria-labelledby="registry-availability-heading">
        <div class="builder">
          <h2 id="registry-availability-heading">{{ translate(collection.enabled ? 'admin.registry_disable' : 'admin.registry_enable') }}</h2>
          <p>{{ translate(collection.enabled ? 'admin.registry_disable_effect' : 'admin.registry_enable_effect') }}</p>
          <div class="builder-selection">
            <button type="button" class="admin-secondary" autofocus @click="availabilityDialog?.close()">{{ translate('admin.cancel') }}</button>
            <button type="button" @click="changeAvailability">{{ translate(collection.enabled ? 'admin.registry_disable' : 'admin.registry_enable') }}</button>
          </div>
        </div>
      </dialog>
    </template>
    <details v-if="collection?.applied_access">
      <summary>{{ translate('admin.registry_access_applied') }}</summary>
      <ul>
        <li v-for="grant in collection.applied_access.grants" :key="`${grant.operation}:${grant.object_scope}`">
          {{ operationName(grant.operation, translate) }}
          <template v-if="grant.object_scope"> · {{ translate(`admin.role_rule_scope_${grant.object_scope}`) }}</template>
          <template v-if="grant.field_ids?.length">
            · {{ collection.fields.filter(field => grant.field_ids?.includes(field.field_id)).map(fieldLabel).join(', ') }}
          </template>
        </li>
      </ul>
    </details>
    <button v-if="conflict || (failure && !canEdit)" type="button" class="admin-secondary" :disabled="busy" @click="load">
      {{ translate(dirty ? 'admin.reload_settings' : 'admin.registry_reload') }}
    </button>
    <div v-if="canEdit && isCustom" class="builder-control">
      <label for="registry-collection-name">{{ translate('admin.registry_collection_name') }}</label>
      <input id="registry-collection-name" ref="collectionNameInput" v-model="newName" form="registry-fields" required maxlength="120" dir="auto" :readonly="busy">
    </div>
    <RegistryIconEditor
      v-if="canEdit" ref="iconEditor" v-model="iconSvg"
      :existing="Boolean(collection)" :busy="busy" :save-blocked="busy || limited || iconInvalid || iconConflict || !changedIcon"
      :invalid="iconInvalid" :conflict="iconConflict" :failure="iconFailure" :saved="iconSaved"
      :preview-icon="previewIcon" :preview-unavailable="previewUnavailable" :translate="translate"
      @edited="icon.clearDraftFeedback()" @save="saveIcon" @reload="reloadIcon"
    />
    <form v-if="canEdit" id="registry-fields" ref="form" class="admin-domain" @submit.prevent="save" @invalid.capture="openFieldDetails($event.target)">
      <AdminDomainHeader
        v-if="collection" :title="translate('admin.registry_schema') + ' ' + collection.schema_no"
        :save-label="translate('admin.registry_save')" :disabled="busy || conflict || limited || !fieldsDirty"
        class="builder-schema"
      />
      <div v-if="collection" class="builder-schema">
        <p>{{ translate('admin.registry_schema_help') }}</p>
      </div>
      <p v-if="failure" role="alert">{{ translate(failure) }}</p>
      <p v-if="feedback"><output ref="fieldStatus" tabindex="-1">{{ translate(feedback) }}</output></p>
      <RegistryFieldRow
        v-for="row in visibleRows" :key="row.id" v-model="row.field"
        :row-id="row.id" :locked="row.locked" :existing="Boolean(collection)" :busy="busy"
        :targets="targets" :collection-label="collectionLabel" :language="translationLanguage"
        :text="fieldEditorText(row)" :translate="translate"
        @translation="(part, value) => updateLabel(row.field, part, value)" @remove="removeField(row.id)"
      />
      <button ref="addButton" type="button" class="admin-secondary" :disabled="busy || rows.length >= 64" @click="addField">
        {{ translate('admin.registry_add_field') }}
      </button>
      <label v-if="!collection || hasNewFields" class="builder-check">
        <input v-model="initialize" type="checkbox" :disabled="busy">
        {{ translate(collection ? 'admin.registry_new_field_access' : 'admin.registry_initial_access') }}
      </label>
      <button v-if="!collection" type="submit" :disabled="busy || conflict || limited || iconInvalid">
        {{ translate('admin.registry_create') }}
      </button>
    </form>
  </section>
</template>
