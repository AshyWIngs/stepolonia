<script setup lang="ts">
import { AdminContentError } from '~/utils/admin-request'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { components } from '@stepolonia/sdk'
import { commandDraft, readDraft, saveDraft } from '~/utils/admin-content'
import type { DraftCommand, EditableDraft, EditorField, EditorMetadata, Scalar } from '~/utils/admin-content'
import { draftPatch, ownValue, savedFieldValue } from '~/utils/draft-edit-buffer'
import type { FieldEdit } from '~/utils/draft-edit-buffer'

const props = defineProps<{
  typeKey: string
  itemId: string
  csrf: string
  uiLanguage: string
  initialLanguage: string
  defaultLanguage: string
  languages: components['schemas']['Language'][]
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  close: [], 'step-up': [], 'session-changed': [], 'language-changed': [locale: string], changed: [],
  'editing-state': [state: { dirty: boolean, busy: boolean }],
}>()
const draft = ref<EditableDraft>()
const metadata = ref<EditorMetadata>()
const heading = ref<HTMLElement | null>(null)
const form = ref<HTMLFormElement | null>(null)
const confirmationHeading = ref<HTMLElement | null>(null)
const edits = ref(new Map<string, FieldEdit>())
const invalid = ref(new Set<string>())
const selectedLanguage = ref('')
const selectedPublications = ref<string[]>([])
const confirmation = ref<'publish' | 'unpublish' | 'reload' | 'close' | 'close_assignments' | null>(null)
const busy = ref(false)
const conflict = ref(false)
const feedback = ref('')
const failure = ref('')
const assignmentsOpen = ref(false)
const assignmentsState = ref({ dirty: false, busy: false })
const assignmentsButton = ref<HTMLElement>()
let active = true

const sharedFields = computed(() => metadata.value?.fields.filter(field => field.scope === 'shared') ?? [])
const contentFieldOrder = ['title', 'slug', 'body']
const localeFields = computed(() => (metadata.value?.fields.filter(field => field.scope === 'localized') ?? [])
  .sort((left, right) => fieldOrder(left.key) - fieldOrder(right.key)))
const hiddenLanguages = computed(() => Object.keys(draft.value?.locales ?? {}).filter(code => !activeLanguage(code)))
const readyLanguages = computed(() => props.languages.filter(language => localeReady(language.code)))
const publicLinks = computed(() => metadata.value?.published.filter(item => /^\/[a-z0-9-]+\/[a-z0-9-]+\/[a-z0-9-]+$/u.test(item.canonical_url)) ?? [])
const locked = computed(() => busy.value || conflict.value || edits.value.size > 0)
const outdated = computed(() => draft.value?.schema_no !== metadata.value?.current_schema_no)
const hasChanges = computed(() => edits.value.size > 0 || assignmentsState.value.dirty)
const editorBusy = computed(() => busy.value || assignmentsState.value.busy)
watch([hasChanges, editorBusy], ([dirty, isBusy]) => {
  emit('editing-state', { dirty, busy: isBusy })
})

function fieldOrder(key: string) {
  const position = contentFieldOrder.indexOf(key)
  return position >= 0 ? position : contentFieldOrder.length
}

function activeLanguage(code: string) {
  return props.languages.some(language => language.code === code)
}

function localeReady(code: string) {
  return metadata.value?.ready_locales.includes(code) === true && ownValue(draft.value?.locales, code)?.ready != null
}

function showError(error: unknown) {
  if (!(error instanceof AdminContentError)) {
    failure.value = 'admin.unavailable'
    return
  }
  if (error.code === 'step_up_required') {
    emit('step-up')
    failure.value = 'admin.step_up_required'
  }
  else if (error.status === 401 || error.code === 'csrf_failed') {
    emit('session-changed')
    failure.value = 'admin.changed'
  }
  else if (error.status === 409) {
    conflict.value = ['version_conflict', 'schema_conflict', undefined].includes(error.code)
    failure.value = conflict.value ? 'admin.content_conflict' : 'admin.content_transition_failed'
  }
  else if (error.status === 403 || error.status === 404) {
    failure.value = 'admin.access_denied'
  }
  else {
    failure.value = error.status === 400 || error.status === 413 ? 'admin.content_invalid' : 'admin.unavailable'
  }
}

async function refresh() {
  const result = await readDraft(props.typeKey, props.itemId)
  if (!active) return
  draft.value = result.draft
  metadata.value = result.metadata
  if (!activeLanguage(selectedLanguage.value) && !ownValue(result.draft.locales, selectedLanguage.value)) {
    const available = props.languages.map(language => language.code)
    const populated = Object.keys(result.draft.locales).filter(code => {
      const locale = ownValue(result.draft.locales, code)
      return locale && Object.keys(locale.values).length > 0
    })
    const preferences = [props.uiLanguage, props.defaultLanguage, ...available, ...hiddenLanguages.value]
    selectedLanguage.value = [props.initialLanguage, ...preferences.filter(code => populated.includes(code)), ...preferences]
      .find(code => activeLanguage(code) || ownValue(result.draft.locales, code)) ?? ''
  }
  conflict.value = false
}

function changeDraftLanguage() {
  invalid.value.clear()
  emit('language-changed', selectedLanguage.value)
}

async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  feedback.value = ''
  failure.value = ''
  try { await work() }
  catch (error: unknown) { if (active) showError(error) }
  finally { busy.value = false }
}

async function closeAssignments() {
  assignmentsState.value = { dirty: false, busy: false }
  assignmentsOpen.value = false
  await nextTick()
  assignmentsButton.value?.focus()
}

async function assignmentsSaved() {
  await closeAssignments()
  await run(refresh)
}

function description(field: EditorField) {
  const labels = ownValue(field.display_labels, props.uiLanguage)
  const builtIn = field.scope === 'localized' && field.visibility === 'public'
  return {
    label: labels?.label || (builtIn && fieldOrder(field.key) < contentFieldOrder.length
      ? props.translate(`admin.content_field_${field.key}`)
      : field.key),
    help: labels?.help || fieldHelp(field, builtIn),
  }
}

function fieldHelp(field: EditorField, builtIn: boolean) {
  if (field.kind === 'relation') return props.translate('admin.content_relation_help')
  if (builtIn && field.key === 'slug' && field.config.slug) return props.translate('admin.content_field_slug_help')
  return undefined
}

function editKey(field: EditorField, locale: string) {
  return `${field.field_id}/${locale}`
}

function valueFor(field: EditorField, locale: string) {
  const edit = edits.value.get(editKey(field, locale))
  if (edit) return edit.value
  return draft.value ? savedFieldValue(draft.value, field, locale) : undefined
}

function edit(field: EditorField, locale: string, value: Scalar | undefined) {
  if (!draft.value || !field.editable) return
  const key = editKey(field, locale)
  if (value === savedFieldValue(draft.value, field, locale)) edits.value.delete(key)
  else edits.value.set(key, { field, locale, value })
  invalid.value.delete(field.field_id)
}

function clearRelation(field: EditorField, locale: string) {
  if (draft.value && field.editable && field.kind === 'relation') {
    edits.value.set(editKey(field, locale), { field, locale, value: undefined })
  }
}

async function save() {
  const source = draft.value
  if (!source || edits.value.size === 0 || conflict.value) return
  await run(async () => {
    const saved = await saveDraft(props.typeKey, source, props.csrf, draftPatch(edits.value.values()))
    if (!active) return
    draft.value = saved
    edits.value.clear()
    invalid.value.clear()
    feedback.value = 'admin.draft_saved'
    emit('changed')
  })
}

async function markReady(shared: boolean) {
  const locale = selectedLanguage.value
  if (!shared && (!activeLanguage(locale) || !ownValue(draft.value?.locales, locale))) return
  invalid.value = new Set([
    ...sharedFields.value.filter(field => field.required && valueFor(field, '') === undefined),
    ...(shared ? [] : localeFields.value.filter(field => field.required && valueFor(field, locale) === undefined)),
  ].map(field => field.field_id))
  if (invalid.value.size) {
    failure.value = 'admin.content_required_missing'
    await nextTick()
    form.value?.querySelector<HTMLElement>('[aria-invalid="true"]:not([readonly]):not(:disabled)')?.focus()
    return
  }
  await transition(shared ? 'ready-shared' : 'ready')
}

async function transition(action: DraftCommand) {
  const source = draft.value
  if (!source || locked.value) return
  const details = new Map<DraftCommand, object>([
    ['ready', { locale: selectedLanguage.value }],
    ['publish', { intended_locales: selectedPublications.value }],
    ['adopt-schema', { schema_no: metadata.value?.current_schema_no }],
  ])
  const messages = new Map<DraftCommand, string>([
    ['publish', 'admin.content_published'],
    ['unpublish', 'admin.content_unpublished'],
    ['adopt-schema', 'admin.content_schema_adopted'],
  ])
  await run(async () => {
    await commandDraft(props.typeKey, source, action, props.csrf, details.get(action) ?? {})
    if (!active) return
    confirmation.value = null
    emit('changed')
    await refresh()
    feedback.value = messages.get(action) ?? 'admin.content_ready_saved'
  })
}

async function ask(question: NonNullable<typeof confirmation.value>) {
  if (question === 'publish') selectedPublications.value = readyLanguages.value.map(language => language.code)
  confirmation.value = question
  await nextTick()
  confirmationHeading.value?.focus()
}

function confirm() {
  switch (confirmation.value) {
    case 'close': emit('close'); return
    case 'close_assignments':
      confirmation.value = null
      return closeAssignments()
    case 'reload':
      return run(async () => {
        await refresh()
        if (!active) return
        edits.value.clear()
        invalid.value.clear()
        confirmation.value = null
      })
    case 'publish':
      if (selectedPublications.value.length) return transition('publish')
      return
    case 'unpublish':
      return transition('unpublish')
  }
}

onMounted(() => {
  heading.value?.focus()
  void run(refresh)
})
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="draft-editor" aria-labelledby="draft-editor-heading" :aria-busy="busy">
    <header class="draft-bar">
      <h2 id="draft-editor-heading" ref="heading" tabindex="-1">{{ translate('admin.content_editor') }}</h2>
      <button type="button" class="admin-secondary" :disabled="editorBusy" @click="hasChanges ? ask('close') : emit('close')">
        {{ translate('admin.content_back') }}
      </button>
    </header>
    <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
    <p v-else-if="feedback" role="status">{{ translate(feedback) }}</p>
    <p v-if="failure" role="alert" class="draft-error">{{ translate(failure) }}</p>
    <button v-if="conflict || (!draft && failure)" type="button" :disabled="busy" @click="edits.size ? ask('reload') : run(refresh)">
      {{ translate('admin.content_reload') }}
    </button>

    <AdminAssignees
      v-if="assignmentsOpen" :csrf="csrf" :type-key="typeKey" :item-id="itemId" :translate="translate"
      :inert="confirmation !== null"
      @step-up="emit('step-up')" @session-changed="emit('session-changed')" @saved="assignmentsSaved"
      @close="assignmentsState.dirty ? ask('close_assignments') : closeAssignments()"
      @editing-state="assignmentsState = $event"
    />
    <template v-else-if="draft && metadata">
      <button ref="assignmentsButton" type="button" class="admin-secondary" :disabled="locked || confirmation !== null" @click="assignmentsOpen = true">
        {{ translate('admin.assignees') }}
      </button>
      <section aria-labelledby="draft-published-heading">
        <h3 id="draft-published-heading">{{ translate('admin.content_public_links') }}</h3>
        <p v-if="publicLinks.length === 0">{{ translate('admin.content_not_published') }}</p>
        <p v-else>{{ translate('admin.content_links_new_tab') }}</p>
        <ul v-if="publicLinks.length">
          <li v-for="item in publicLinks" :key="item.language">
            <a :href="item.canonical_url" :hreflang="item.language" target="_blank" rel="noopener">{{ item.canonical_url }}</a>
          </li>
        </ul>
      </section>
      <p v-if="outdated" role="status">{{ translate('admin.content_schema_outdated') }}</p>

      <form ref="form" @submit.prevent="save">
        <fieldset v-if="sharedFields.length" :disabled="busy || confirmation !== null">
          <legend>{{ translate('admin.content_shared') }}</legend>
          <DraftFieldControl
            v-for="field in sharedFields" :key="field.field_id" :field="field" v-bind="description(field)"
            :value="valueFor(field, '')" :invalid="invalid.has(field.field_id)" :translate="translate"
            @change="edit(field, '', $event)" @clear-relation="clearRelation(field, '')"
          />
        </fieldset>
        <fieldset v-if="languages.length || hiddenLanguages.length" :disabled="busy || confirmation !== null">
          <legend>{{ translate('admin.content_translation') }}</legend>
          <label for="content-edit-language">{{ translate('admin.language') }}</label>
          <select id="content-edit-language" v-model="selectedLanguage" @change="changeDraftLanguage">
            <option v-for="language in languages" :key="language.code" :value="language.code" :lang="language.code" :dir="language.direction">
              {{ language.native_name }}
            </option>
            <option v-for="code in hiddenLanguages" :key="code" :value="code">{{ code }}</option>
          </select>
          <p v-if="selectedLanguage && !activeLanguage(selectedLanguage)">{{ translate('admin.content_language_disabled') }}</p>
          <p v-if="draft.shared_ready_seq">{{ translate('admin.content_shared_ready') }}</p>
          <p v-if="localeReady(selectedLanguage)">{{ translate('admin.content_locale_ready') }}</p>
          <template v-if="selectedLanguage">
            <DraftFieldControl
              v-for="field in localeFields" :key="`${field.field_id}/${selectedLanguage}`" :field="field" v-bind="description(field)"
              :value="valueFor(field, selectedLanguage)" :invalid="invalid.has(field.field_id)" :translate="translate"
              @change="edit(field, selectedLanguage, $event)" @clear-relation="clearRelation(field, selectedLanguage)"
            />
          </template>
        </fieldset>
        <div class="draft-bar">
          <button type="submit" :disabled="busy || conflict || !edits.size || confirmation !== null">{{ translate('admin.save_draft') }}</button>
          <button
            v-if="metadata.actions.shared_ready && !outdated" type="button" class="admin-secondary" :disabled="locked || confirmation !== null"
            @click="markReady(true)"
          >{{ translate('admin.content_mark_shared') }}</button>
          <button
            v-if="metadata.actions.ready && !outdated && ownValue(draft.locales, selectedLanguage) && activeLanguage(selectedLanguage)"
            type="button" class="admin-secondary" :disabled="locked || confirmation !== null" @click="markReady(false)"
          >{{ translate('admin.content_mark_locale') }}</button>
          <button
            v-if="metadata.actions.adopt && outdated"
            type="button" class="admin-secondary" :disabled="locked || confirmation !== null" @click="transition('adopt-schema')"
          >{{ translate('admin.content_adopt_schema') }}</button>
          <button
            v-if="metadata.actions.publish && !outdated" type="button" :disabled="locked || !draft.shared_ready_seq || !readyLanguages.length || confirmation !== null"
            @click="ask('publish')"
          >{{ translate('admin.content_publish') }}</button>
          <button
            v-if="metadata.actions.unpublish && draft.publication_id" type="button" class="admin-secondary"
            :disabled="locked || confirmation !== null" @click="ask('unpublish')"
          >{{ translate('admin.content_unpublish') }}</button>
        </div>
      </form>
    </template>

    <section v-if="confirmation" aria-labelledby="draft-confirmation-heading">
      <h3 id="draft-confirmation-heading" ref="confirmationHeading" tabindex="-1">
        {{ translate(confirmation === 'close_assignments' ? 'admin.leave_unsaved' : `admin.content_confirm_${confirmation}`) }}
      </h3>
      <fieldset v-if="confirmation === 'publish'" :disabled="busy">
        <legend>{{ translate('admin.content_publish_languages') }}</legend>
        <label v-for="language in readyLanguages" :key="language.code" class="draft-checkbox">
          <input v-model="selectedPublications" type="checkbox" :value="language.code">
          <span :lang="language.code" :dir="language.direction">{{ language.native_name }}</span>
        </label>
      </fieldset>
      <div class="draft-bar">
        <button type="button" :disabled="busy || (confirmation === 'publish' && !selectedPublications.length)" @click="confirm">
          {{ translate('admin.content_confirm') }}
        </button>
        <button type="button" class="admin-secondary" :disabled="busy" @click="confirmation = null">{{ translate('admin.cancel') }}</button>
      </div>
    </section>
  </section>
</template>

<style scoped>
.draft-editor, form, fieldset {
  display: grid;
  gap: 1rem;
  min-width: 0;
}

.draft-editor a, .draft-editor p {
  overflow-wrap: anywhere;
}

.draft-editor a {
  color: var(--admin-focus);
}

.draft-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: .65rem;
}

fieldset {
  box-sizing: border-box;
  margin: 0;
  padding: .85rem;
  border: 1px solid var(--admin-border);
  border-radius: .5rem;
}

select {
  min-width: 0;
  min-height: 44px;
  padding: .5rem;
  color: var(--admin-text);
  background: var(--admin-card);
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  font: inherit;
}

.draft-checkbox {
  display: flex;
  align-items: center;
  gap: .65rem;
}

.draft-checkbox input {
  width: 1.25rem;
  min-height: 1.25rem;
  margin: 0;
}

.draft-error {
  color: var(--admin-error);
}
</style>
