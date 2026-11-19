<script setup lang="ts">
import { AdminContentError } from '~/utils/admin-request'
import { operationName } from '~/utils/admin-role-rules'
import { ownValue } from '~/utils/draft-edit-buffer'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { usePublicApi } from '~/composables/usePublicApi'
import { hasSVGViewBox, svgNamespace, useSvgImagePreview } from '~/composables/useSvgImagePreview'
import { changeCollectionFields, createCollection, listCollections, readManagedCollection, saveCollectionIcon, setCollectionAvailability } from '~/utils/admin-content'
import type { CollectionChoice, FieldKind, ManagedCollection, RegistryField, RegistryRenderer } from '~/utils/admin-content'
import { suggestSlug, type SlugRule } from '~/utils/slug-suggestion'
import type { PortalLanguage } from '~/utils/admin-site-settings'

interface BuilderRow {
  id: number
  field: RegistryField
  locked: boolean
  originalLabels?: NonNullable<RegistryField['display_labels']>
}

type DisplayPart = 'label' | 'help'
type TranslationLanguage = Pick<PortalLanguage, 'code' | 'native_name'> & Partial<Pick<PortalLanguage, 'direction'>>

const props = defineProps<{
  csrf: string
  uiLanguage: string
  languages: PortalLanguage[]
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
const iconSvg = ref('')
const iconInvalid = ref(false)
const iconConflict = ref(false)
const iconFailure = ref('')
const iconSaved = ref(false)
const iconStatus = ref<HTMLOutputElement>()
const iconInput = ref<HTMLTextAreaElement>()
const availabilityDialog = ref<HTMLDialogElement>()
let creationSuffix: string | undefined
const collection = ref<ManagedCollection>()
const rows = ref<BuilderRow[]>([])
const collectionChooser = ref<HTMLSelectElement>()
const collectionNameInput = ref<HTMLInputElement>()
const form = ref<HTMLFormElement>()
const addButton = ref<HTMLButtonElement>()
const translationLocale = ref(props.uiLanguage)
const contentLanguages = ref<PortalLanguage[]>([])
const defaults = ref<{ locale: string, messages: Record<string, string> }>()
const { client, request } = usePublicApi()
const initialize = ref(false)
const busy = ref(false)
const failure = ref('')
const feedback = ref('')
const fieldStatus = ref<HTMLOutputElement>()
const conflict = ref(false)
const limited = ref(false)
let retryTimer: ReturnType<typeof setTimeout> | undefined
const kinds: FieldKind[] = ['string', 'text', 'markdown', 'boolean', 'integer', 'decimal', 'datetime', 'enum', 'relation']
const builtInFields = new Set(['title', 'slug', 'body'])
let nextRowID = 0
let active = true

const available = computed(() => props.renderers.filter(item => item.available))
const renderer = computed(() => available.value.find(item => item.type_key === selected.value))
const isCustom = computed(() => !props.collectionKey && !renderer.value?.type_key)
const canEdit = computed(() => rows.value.length > 0 && (collection.value
  ? collection.value.enabled
  : !props.collectionKey && Boolean(renderer.value)))
const hasNewFields = computed(() => rows.value.some(row => !row.locked))
const visibleRows = computed(() => rows.value.filter(({ field }) =>
  field.key !== 'slug' || field.config?.slug !== true,
))
const savedIcon = computed(() => collection.value?.icon_svg ?? '')
const changedIcon = computed(() => iconSvg.value !== savedIcon.value)
const { svg: previewIcon, unavailable: previewUnavailable } = useSvgImagePreview(iconSvg, savedIcon)
const fieldsDirty = computed(() => initialize.value || hasNewFields.value || rows.value.some(labelsChanged))
const dirty = computed(() => Boolean(newName.value.trim()) || changedIcon.value || fieldsDirty.value)
watch([dirty, busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const translationLanguages = computed(() => {
  const result = new Map<string, TranslationLanguage>()
  for (const language of [...props.languages, ...contentLanguages.value]) result.set(language.code, language)
  for (const field of collection.value?.fields ?? renderer.value?.fields ?? []) {
    for (const code of Object.keys(field.display_labels ?? {})) {
      if (!result.has(code)) result.set(code, { code, native_name: code })
    }
  }
  return [...result.values()]
})
const translationLanguage = computed(() => translationLanguages.value.find(language => language.code === translationLocale.value))

watch(translationLanguages, (languages) => {
  if (!translationLanguage.value) {
    translationLocale.value = languages.find(language => language.code === props.uiLanguage)?.code ?? languages[0]?.code ?? ''
  }
}, { immediate: true })

watch([translationLocale, () => props.uiLanguage, () => props.languages], async ([language, uiLanguage, catalogs], _, onCleanup) => {
  if (language === uiLanguage || !catalogs.some(catalog => catalog.code === language)) return
  defaults.value = undefined
  let cancelled = false
  onCleanup(() => { cancelled = true })
  try {
    const result = await request(() => client.getSite({ query: { lang: language } }))
    if (!cancelled && result.data?.language.code === language) {
      defaults.value = { locale: language, messages: result.data.messages }
    }
  }
  catch {
    // The built-in examples are optional; all translations remain editable.
  }
}, { immediate: true })

function markInvalidIcon() {
  iconInvalid.value = true
  void nextTick(() => iconInput.value?.focus())
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
  if (forIcon) {
    iconFailure.value = ''
    iconSaved.value = false
  } else {
    failure.value = ''
    feedback.value = ''
  }
  try { await work() }
  catch (error: unknown) { if (active) showError(error, forIcon) }
  finally { busy.value = false }
}

function setFields(list: RegistryField[], keepDetails = false) {
  const previousIDs = new Map(keepDetails ? rows.value.map(row => [row.field.key, row.id]) : [])
  rows.value = list.map(field => ({
    id: previousIDs.get(field.key) ?? nextRowID++,
    locked: true,
    originalLabels: field.display_labels,
    field: {
      key: field.key,
      kind: field.kind,
      scope: field.scope,
      visibility: field.visibility,
      required: field.required,
      target_type_id: field.target_type_id,
      config: field.config,
      display_labels: field.display_labels,
    },
  }))
  initialize.value = false
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
  iconSvg.value = loaded?.icon_svg ?? ''
  iconInvalid.value = false
  iconConflict.value = false
  iconFailure.value = ''
  iconSaved.value = false
  setFields(loaded?.fields ?? renderer.value?.fields ?? [])
}

async function load() {
  await run(async () => {
    await openRenderer()
    const types = await listCollections()
    if (active) targets.value = types.items
  })
}

async function loadTranslationLanguages() {
  try {
    const result = await request(() => client.listLanguages())
    if (active && result.data) contentLanguages.value = result.data
  }
  catch {
    // Published UI languages and stored translations remain available.
  }
}

async function discardChanges() {
  const choice = renderer.value
  if ((!collection.value && !choice) || busy.value) return
  newName.value = ''
  iconSvg.value = savedIcon.value
  iconInvalid.value = false
  setFields(collection.value?.fields ?? choice?.fields ?? [])
  feedback.value = ''
  failure.value = ''
  await nextTick()
  const focusTarget = collectionChooser.value ?? collectionNameInput.value ?? addButton.value
  focusTarget?.focus()
}

async function addField() {
  const id = nextRowID++
  rows.value.push({ id, locked: false, field: { key: '', kind: 'string', scope: 'localized', visibility: 'private', required: false } })
  await nextTick()
  const key = form.value?.querySelector<HTMLInputElement>(`#registry-key-${id}`)
  if (key) {
    openFieldDetails(key)
    key.focus()
  }
}

async function removeField(id: number) {
  rows.value = rows.value.filter(row => row.id !== id)
  if (collection.value && !hasNewFields.value) initialize.value = false
  await nextTick()
  addButton.value?.focus()
}

function openFieldDetails(control: EventTarget | null) {
  if (!(control instanceof HTMLElement)) return
  const details = control.closest('details')
  if (details) details.open = true
}

function builtInKey(field: RegistryField, part: DisplayPart) {
  if (part === 'help' && field.kind === 'relation') return 'admin.content_relation_help'
  if (field.scope !== 'localized' || field.visibility !== 'public' || !builtInFields.has(field.key)) return undefined
  if (part === 'label') return `admin.content_field_${field.key}`
  if (field.key === 'slug' && field.config?.slug) return 'admin.content_field_slug_help'
  return undefined
}

function fieldLabel(field: RegistryField) {
  const custom = ownValue(field.display_labels, props.uiLanguage)?.label
  if (custom?.trim()) return custom
  const key = builtInKey(field, 'label')
  const localized = key && props.translate(key)
  return localized && localized !== key ? localized : field.key
}

function summaryName({ field, locked }: BuilderRow) {
  return fieldLabel(field) || props.translate(locked ? 'admin.registry_unnamed_field' : 'admin.registry_new_field')
}

function displayValue(field: RegistryField, part: DisplayPart) {
  return ownValue(field.display_labels, translationLocale.value)?.[part] ?? ''
}

function displayDefault(field: RegistryField, part: DisplayPart) {
  const key = builtInKey(field, part)
  if (!key) return part === 'label' ? field.key : undefined
  if (translationLocale.value === props.uiLanguage) return props.translate(key)
  if (defaults.value?.locale === translationLocale.value) return defaults.value.messages[key]
  return undefined
}

function helpExample(field: RegistryField) {
  return displayValue(field, 'help') ? undefined : displayDefault(field, 'help')
}

function labelsChanged({ field, originalLabels }: BuilderRow) {
  const current = field.display_labels
  if (current === originalLabels) return false
  const entries = Object.entries(current ?? {})
  return entries.length !== Object.keys(originalLabels ?? {}).length || entries.some(([locale, { label, help }]) => {
    const original = ownValue(originalLabels, locale)
    return label !== original?.label || help !== original?.help
  })
}

function updateLabel(field: RegistryField, part: DisplayPart, event: Event) {
  const control = event.currentTarget
  if (!(control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement)) return
  const locale = translationLocale.value
  const entries = new Map(Object.entries(field.display_labels ?? {}))
  const current = entries.get(locale) ?? {}
  const label = part === 'label' ? control.value : current.label
  const help = part === 'help' ? control.value : current.help
  if (label || help) entries.set(locale, { label: label || undefined, help: help || undefined })
  else entries.delete(locale)
  field.display_labels = Object.fromEntries(entries)
}

function updateCodes(field: RegistryField, event: Event) {
  const control = event.currentTarget
  if (control instanceof HTMLInputElement) {
    field.config = { codes: control.value.split(',').map(code => code.trim()).filter(Boolean) }
  }
}

function changeKind(field: RegistryField) {
  field.config = undefined
  field.target_type_id = undefined
}

async function save() {
  const wasPresent = collection.value
  if (!canEdit.value || conflict.value || limited.value || (wasPresent ? !fieldsDirty.value : iconInvalid.value)) return
  if (!wasPresent && changedIcon.value && iconSvg.value.trim() && !hasSVGViewBox(iconSvg.value)) {
    failure.value = ''
    feedback.value = ''
    markInvalidIcon()
    return
  }
  await run(async () => {
    const fields = rows.value.map(row => row.field)
    const saved = wasPresent ? await saveFieldChanges(wasPresent)
      : await saveNewCollection(fields)
    if (!active || !saved) return
    acceptFieldResult(saved, wasPresent)
    newName.value = ''
    setFields(saved.fields, true)
    feedback.value = wasPresent ? 'admin.registry_revised' : 'admin.registry_created'
    emit('saved', saved)
  })
  if (active && feedback.value) await nextTick(() => fieldStatus.value?.focus())
}

function acceptFieldResult(saved: ManagedCollection, before?: ManagedCollection) {
  const pendingIcon = before && changedIcon.value && iconSvg.value !== (saved.icon_svg ?? '')
  if (!pendingIcon) {
    iconSvg.value = saved.icon_svg ?? ''
    collection.value = saved
    if (iconConflict.value) {
      iconConflict.value = false
      iconFailure.value = ''
    }
    return
  }
  collection.value = { ...saved, icon_version: before.icon_version }
  if (saved.icon_version !== before.icon_version) {
    iconConflict.value = true
    iconFailure.value = 'admin.registry_conflict'
  }
}

function saveFieldChanges(current: ManagedCollection) {
  const additions = rows.value.filter(row => !row.locked).map(row => row.field)
  const labels = new Map(rows.value.filter(row => row.locked && labelsChanged(row)).map(row => [row.field.key, row.field.display_labels]))
  const updates = current.fields.filter(field => labels.has(field.key)).map(field => ({
    key: field.key,
    expected_version: field.label_version,
    display_labels: labels.get(field.key) ?? {},
  }))
  return changeCollectionFields(current, additions, updates, initialize.value, props.csrf)
}

async function saveIcon() {
  const current = collection.value
  if (!current || !changedIcon.value || busy.value || limited.value || iconConflict.value || iconInvalid.value) return
  if (iconSvg.value.trim() && !hasSVGViewBox(iconSvg.value)) {
    markInvalidIcon()
    return
  }
  await run(async () => {
    const result = await saveCollectionIcon(current, iconSvg.value, props.csrf)
    if (!active) return
    const version = current.version === result.previous_version ? result.version : current.version
    const saved = { ...current, version, icon_svg: result.icon_svg, icon_version: result.icon_version }
    collection.value = saved
    iconSvg.value = result.icon_svg
    iconSaved.value = true
    emit('saved', saved)
  }, true)
  if (active && iconSaved.value) await nextTick(() => iconStatus.value?.focus())
}

async function reloadIcon() {
  const current = collection.value
  if (!current || busy.value) return
  await run(async () => {
    const fresh = await readManagedCollection(current.type_key)
    if (!active) return
    const reloaded = { ...current, icon_svg: fresh.icon_svg, icon_version: fresh.icon_version }
    collection.value = reloaded
    iconConflict.value = false
    emit('saved', reloaded)
  }, true)
  if (active && !iconConflict.value) await nextTick(() => iconInput.value?.focus())
}

function saveNewCollection(fields: RegistryField[]) {
  const choice = renderer.value
  if (!choice) return
  if (choice.type_key) return createCollection(choice, fields, initialize.value, props.csrf, { icon_svg: iconSvg.value })
  const name = newName.value.trim()
  if (!name) return
  creationSuffix ??= crypto.randomUUID().replaceAll('-', '').slice(0, 12)
  const slug = suggestSlug(name, props.uiLanguage, props.slugRules, creationSuffix, 51)
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
  void loadTranslationLanguages()
})
onBeforeUnmount(() => {
  active = false
  clearTimeout(retryTimer)
})
</script>

<template>
  <section class="builder" :aria-busy="busy">
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
    <component :is="collection ? 'form' : 'div'" v-if="canEdit" class="admin-domain" @submit.prevent="saveIcon">
      <AdminDomainHeader
        v-if="collection" :title="translate('admin.registry_icon')" :save-label="translate('admin.registry_icon_save')"
        :disabled="busy || limited || iconInvalid || iconConflict || !changedIcon"
      />
      <div class="builder-control builder-icon-editor">
        <label for="registry-collection-icon" :class="{ 'builder-icon-label--named': collection }">{{ translate('admin.registry_icon') }}</label>
        <div class="builder-icon-preview">
          <span v-if="!previewUnavailable" class="builder-icon-sample" data-testid="collection-icon-preview"><AdminIcon name="collection" :icon-svg="previewIcon" /></span>
          <span v-else>{{ translate('admin.svg_preview_unavailable') }}</span>
        </div>
        <p id="registry-icon-help" class="builder-hint">
          {{ translate('admin.registry_icon_help') }}
          <br><code>viewBox="0 0 24 24"</code> · 20 × 20 px
        </p>
        <textarea
          id="registry-collection-icon" ref="iconInput" v-model="iconSvg" :readonly="busy" maxlength="4096" rows="4"
          dir="ltr" spellcheck="false" :aria-invalid="iconInvalid || undefined"
          :aria-describedby="iconInvalid ? 'registry-icon-help registry-icon-error' : 'registry-icon-help'"
          @input="iconInvalid = false; iconSaved = false"
        />
        <p v-if="iconInvalid" id="registry-icon-error" role="alert">
          {{ translate('admin.registry_icon_invalid') }}
          <code>xmlns="{{ svgNamespace }}"</code>
        </p>
        <button
          v-if="iconSvg" type="button" class="admin-secondary" :disabled="busy"
          @click="iconSvg = ''; iconInvalid = false; iconSaved = false; iconInput?.focus()"
        >{{ translate('admin.registry_icon_default') }}</button>
        <p v-if="iconFailure" role="alert">{{ translate(iconFailure) }}</p>
        <output v-if="iconSaved" ref="iconStatus" tabindex="-1">{{ translate('admin.registry_icon_saved') }}</output>
        <button v-if="iconConflict" type="button" class="admin-secondary" :disabled="busy" @click="reloadIcon">
          {{ translate('admin.settings_scoped_reload') }}
        </button>
      </div>
    </component>
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
      <details
        v-for="row in visibleRows" :key="row.id" class="builder-field"
        :aria-labelledby="`registry-name-${row.id}`"
      >
        <summary>
          <span class="builder-summary">
            <strong :id="`registry-name-${row.id}`" class="builder-name" dir="auto">{{ summaryName(row) }}</strong>
            <span class="builder-metadata">
              <span>{{ translate(`admin.registry_kind_${row.field.kind}`) }}</span>
              <span>{{ translate(`admin.registry_scope_${row.field.scope}`) }}</span>
              <span>{{ translate(`admin.registry_${row.field.visibility}`) }}</span>
              <span>{{ translate(row.field.required ? 'admin.registry_required' : 'admin.registry_optional') }}</span>
            </span>
          </span>
        </summary>
        <fieldset :disabled="busy">
          <p v-if="row.locked" class="builder-hint">{{ translate(collection ? 'admin.registry_field_locked' : 'admin.registry_template_locked') }}</p>
          <div v-else class="builder-field-controls">
            <div class="builder-control">
              <label :for="`registry-key-${row.id}`">{{ translate('admin.registry_field_key') }}</label>
              <input :id="`registry-key-${row.id}`" v-model="row.field.key" required pattern="[a-z][a-z0-9_]{0,63}" dir="ltr">
            </div>
            <div class="builder-control">
              <label :for="`registry-kind-${row.id}`">{{ translate('admin.registry_kind') }}</label>
              <select :id="`registry-kind-${row.id}`" v-model="row.field.kind" @change="changeKind(row.field)">
                <option v-for="kind in kinds" :key="kind" :value="kind">{{ translate(`admin.registry_kind_${kind}`) }}</option>
              </select>
            </div>
            <div class="builder-control">
              <label :for="`registry-scope-${row.id}`">{{ translate('admin.registry_scope') }}</label>
              <select :id="`registry-scope-${row.id}`" v-model="row.field.scope">
                <option value="localized">{{ translate('admin.registry_scope_localized') }}</option>
                <option value="shared">{{ translate('admin.registry_scope_shared') }}</option>
              </select>
            </div>
            <div class="builder-control">
              <label :for="`registry-visibility-${row.id}`">{{ translate('admin.registry_visibility') }}</label>
              <select :id="`registry-visibility-${row.id}`" v-model="row.field.visibility">
                <option value="private">{{ translate('admin.registry_private') }}</option>
                <option value="public">{{ translate('admin.registry_public') }}</option>
              </select>
            </div>
            <div v-if="row.field.kind === 'enum'" class="builder-control">
              <label :for="`registry-codes-${row.id}`">{{ translate('admin.registry_codes') }}</label>
              <input
                :id="`registry-codes-${row.id}`" required :value="row.field.config?.codes?.join(', ')"
                @input="updateCodes(row.field, $event)"
              >
            </div>
            <div v-if="row.field.kind === 'relation'" class="builder-control">
              <label :for="`registry-target-${row.id}`">{{ translate('admin.registry_target') }}</label>
              <select :id="`registry-target-${row.id}`" v-model="row.field.target_type_id" required>
                <option v-for="target in targets" :key="target.type_id" :value="target.type_id">{{ collectionLabel(target) }}</option>
              </select>
            </div>
          </div>
          <template v-if="translationLanguage">
            <div class="builder-field-controls">
              <div class="builder-control">
                <label :for="`registry-label-${row.id}`">{{ translate('admin.registry_label') }}</label>
                <input
                  :id="`registry-label-${row.id}`" :value="displayValue(row.field, 'label')" maxlength="4096"
                  :placeholder="displayDefault(row.field, 'label')" :aria-describedby="`registry-default-${row.id}`"
                  :lang="translationLanguage.code" :dir="translationLanguage.direction ?? 'auto'" @input="updateLabel(row.field, 'label', $event)"
                >
              </div>
              <div class="builder-control">
                <label :for="`registry-help-${row.id}`">{{ translate('admin.registry_help') }}</label>
                <textarea
                  :id="`registry-help-${row.id}`" :value="displayValue(row.field, 'help')" maxlength="4096" rows="2"
                  :aria-describedby="helpExample(row.field) ? `registry-help-default-${row.id} registry-default-${row.id}` : `registry-default-${row.id}`"
                  :lang="translationLanguage.code" :dir="translationLanguage.direction ?? 'auto'" @input="updateLabel(row.field, 'help', $event)"
                />
                <p
                  v-if="helpExample(row.field)" :id="`registry-help-default-${row.id}`"
                  class="builder-hint" :lang="translationLanguage.code" :dir="translationLanguage.direction"
                >{{ helpExample(row.field) }}</p>
              </div>
            </div>
            <p :id="`registry-default-${row.id}`" class="builder-hint">{{ translate('admin.registry_translation_help') }}</p>
          </template>
          <button v-if="!row.locked" type="button" class="admin-secondary" @click="removeField(row.id)">{{ translate('admin.registry_remove_field') }}</button>
        </fieldset>
      </details>
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

<style scoped>
.builder-icon-label--named {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

.builder-icon-preview {
  display: flex;
  align-items: center;
  gap: .5rem;
}

.builder-icon-sample {
  display: grid;
  place-items: center;
  flex: none;
  inline-size: 44px;
  block-size: 44px;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
}

.builder-icon-editor textarea {
  font-family: ui-monospace, monospace;
}

.builder-icon-editor button { justify-self: start; }

.builder, .builder-selection, .builder form, .builder fieldset {
  display: grid;
  gap: .55rem;
  min-width: 0;
  overflow-wrap: anywhere;
}

.builder-selection {
  grid-template-columns: repeat(auto-fit, minmax(min(13rem, 100%), 1fr));
  align-items: end;
}

.builder-control, .builder-field-controls {
  display: grid;
  align-items: end;
  gap: .3rem .75rem;
  min-width: 0;
}

.builder-field-controls {
  align-items: start;
  grid-template-columns: repeat(auto-fit, minmax(min(15rem, 100%), 1fr));
  row-gap: .55rem;
}

.builder button {
  justify-self: start;
  white-space: normal;
}

.builder-field {
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
}

.builder-field summary {
  box-sizing: border-box;
  min-block-size: 44px;
  padding: .55rem .75rem;
  cursor: pointer;
}

.builder-summary {
  display: inline-flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: baseline;
  gap: .2rem 1rem;
  inline-size: calc(100% - 1.35em);
  vertical-align: middle;
}

.builder-name { min-width: 0; overflow-wrap: anywhere; }

.builder-metadata {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: .1rem .65rem;
  min-width: 0;
}

.builder-metadata, .builder-hint, .builder-schema p {
  color: var(--admin-muted);
  font-size: .875rem;
}

.builder-metadata span + span::before {
  content: '·';
  margin-inline-end: .65rem;
}

.builder-summary > span { max-width: 100%; }
.builder-hint, .builder-schema p { margin: 0; }

.builder fieldset {
  margin: 0;
  padding: .75rem;
  border: 0;
  border-block-start: 1px solid var(--admin-border);
}

.builder input, .builder select, .builder textarea, .builder button {
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

.builder input { margin-bottom: 0; }
.builder :is(input, select, textarea) { inline-size: 100%; }

.builder textarea { resize: vertical; }

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
  min-block-size: 44px;
  justify-self: start;
}

.builder-check input {
  flex: 0 0 1.1rem;
  inline-size: 1.1rem;
  block-size: 1.1rem;
  min-height: 0;
  margin: 0;
  padding: 0;
}
</style>
