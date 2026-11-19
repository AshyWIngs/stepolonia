<script setup lang="ts">
import { AdminContentError } from '~/utils/admin-request'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { components } from '@stepolonia/sdk'
import { commandDraft, readDraft, saveDraft } from '~/utils/admin-content'
import type { DraftCommand, DraftSummary, EditableDraft, EditorField, EditorMetadata, Scalar } from '~/utils/admin-content'
import { draftPatch, ownValue, savedFieldValue } from '~/utils/draft-edit-buffer'
import type { FieldEdit } from '~/utils/draft-edit-buffer'
import { suggestSlug } from '~/utils/slug-suggestion'
import type { SlugRule } from '~/utils/slug-suggestion'

const props = defineProps<{
  typeKey: string
  itemId: string
  csrf: string
  uiLanguage: string
  initialLanguage: string
  defaultLanguage: string
  slugRules: SlugRule[]
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
const publicationHeading = ref<HTMLElement | null>(null)
const nextAction = ref<HTMLButtonElement | null>(null)
const edits = ref(new Map<string, FieldEdit>())
const invalid = ref(new Set<string>())
const selectedLanguage = ref('')
const selectedPublications = ref<string[]>([])
const confirmation = ref<'publish' | 'unpublish' | 'reload' | 'close' | 'close_assignments' | null>(null)
const reviewPublishedTranslations = ref(false)
const busy = ref(false)
const conflict = ref(false)
const feedback = ref('')
const failure = ref('')
const publicationRefreshRequired = computed(() => failure.value === 'admin.content_publication_unknown' || failure.value === 'admin.content_publication_reload_links')
const assignmentsOpen = ref(false)
const assignmentsState = ref({ dirty: false, busy: false })
const assignmentsButton = ref<HTMLElement>()
let active = true

const sharedFields = computed(() => metadata.value?.fields.filter(field => field.scope === 'shared') ?? [])
const contentFieldOrder = ['title', 'slug', 'body']
const localeFields = computed(() => (metadata.value?.fields.filter(field => field.scope === 'localized') ?? [])
  .sort((left, right) => fieldOrder(left.key) - fieldOrder(right.key)))
const addressField = computed(() => localeFields.value.find(field => field.key === 'slug' && field.config.slug))
const visibleLocaleFields = computed(() => localeFields.value.filter(field => field !== addressField.value))
const hiddenLanguages = computed(() => Object.keys(draft.value?.locales ?? {}).filter(code => !activeLanguage(code)))
const publicLinks = computed(() => metadata.value?.published.filter(item => /^\/[a-z0-9-]+\/[a-z0-9-]+\/[a-z0-9-]+$/u.test(item.canonical_url)) ?? [])
const locked = computed(() => busy.value || conflict.value || edits.value.size > 0)
const outdated = computed(() => draft.value?.schema_no !== metadata.value?.current_schema_no)
const hasChanges = computed(() => edits.value.size > 0 || assignmentsState.value.dirty)
const editorBusy = computed(() => busy.value || assignmentsState.value.busy)
const missingAddress = computed(() => {
  const source = draft.value
  const field = addressField.value
  const locale = selectedLanguage.value
  const title = localeFields.value.find(candidate => candidate.key === 'title')
  if (!source?.locales || source.version !== metadata.value?.version || !field?.editable || !title || !activeLanguage(locale)) return
  if (valueFor(field, locale) !== undefined) return
  const text = valueFor(title, locale)
  const value = typeof text === 'string' ? suggestSlug(text, locale, props.slugRules, props.itemId, field.config.max_length) : ''
  return value ? { field, locale, value } : undefined
})
const needsSave = computed(() => edits.value.size > 0 || Boolean(missingAddress.value))
const hasTranslation = computed(() => activeLanguage(selectedLanguage.value) && Boolean(ownValue(draft.value?.locales, selectedLanguage.value)))
const publishLanguages = computed(() => {
  const actions = metadata.value?.actions
  if (!actions?.publish || outdated.value || (!draft.value?.shared_ready_seq && !actions.shared_ready)) return []
  return props.languages.filter(language => ownValue(draft.value?.locales, language.code) && (actions.ready || localeReady(language.code)))
})
const directPublishLanguage = computed(() => {
  const languages = publishLanguages.value
  const only = languages[0]
  return languages.length === 1 && only?.code === selectedLanguage.value ? only : undefined
})
const publishedChecksMissing = computed(() => metadata.value?.actions.publish === true && reviewPublishedTranslations.value && publicLinks.value.some(item =>
  !publishLanguages.value.some(language => language.code === item.language),
))
const publicationSelectionIncomplete = computed(() => !selectedPublications.value.length ||
  (reviewPublishedTranslations.value && omitsPublishedTranslations(selectedPublications.value)),
)
const canPrepareTranslation = computed(() => {
  const actions = metadata.value?.actions
  const sharedReady = !!draft.value?.shared_ready_seq
  return !outdated.value && hasTranslation.value && !!actions?.ready && (sharedReady ? !localeReady(selectedLanguage.value) : actions.shared_ready)
})
const canPrepareShared = computed(() => !outdated.value && !draft.value?.shared_ready_seq && metadata.value?.actions.shared_ready === true &&
  (!metadata.value.actions.ready || hasTranslation.value),
)
const primaryAction = computed(() => {
  if (needsSave.value) return 'save'
  if (outdated.value) return metadata.value?.actions.adopt ? 'adopt-schema' : ''
  if (publishLanguages.value.length && !publishedChecksMissing.value) return 'publish'
  if (canPrepareTranslation.value) return 'prepare'
  if (canPrepareShared.value) return 'ready-shared'
  return ''
})

const publicationHint = computed(() => {
  if (conflict.value) return 'admin.content_conflict'
  if (needsSave.value) return 'admin.content_save_before_publication'
  if (outdated.value) return metadata.value?.actions.adopt ? 'admin.content_schema_outdated' : 'admin.content_publication_schema_access'
  if (primaryAction.value === 'publish') return reviewPublishedTranslations.value
    ? 'admin.content_publication_translations_need_checks' : 'admin.content_publication_publish_hint'
  if (primaryAction.value === 'prepare' || primaryAction.value === 'ready-shared') return 'admin.content_publication_check_hint'
  return publicationIdleHint()
})

function publicationIdleHint() {
  if (publishedChecksMissing.value) return 'admin.content_publication_await_published_checks'
  if (hasTranslation.value && draft.value?.shared_ready_seq && localeReady(selectedLanguage.value)) return 'admin.content_publication_handoff'
  if (selectedLanguage.value && !activeLanguage(selectedLanguage.value)) return 'admin.content_language_disabled'
  if (!hasTranslation.value && metadata.value?.fields.some(field => field.editable && field.scope === 'localized')) return 'admin.content_publication_add_translation'
  if (metadata.value?.actions.publish) return 'admin.content_publication_await_checks'
  return 'admin.content_publication_access'
}
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

function languageName(code: string) {
  return props.languages.find(language => language.code === code)?.native_name ?? code
}

function omitsPublishedTranslations(locales: string[]) {
  return publicLinks.value.some(item => !locales.includes(item.language))
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
  }
  else if (error.status === 401 || error.code === 'csrf_failed') {
    emit('session-changed')
    failure.value = 'admin.changed'
  }
  else if (error.status === 409) {
    showCandidateConflict(error.code)
  }
  else if (error.status === 403 || error.status === 404) {
    failure.value = 'admin.access_denied'
  }
  else {
    failure.value = error.status === 400 || error.status === 413 ? 'admin.content_invalid' : 'admin.unavailable'
  }
}

function showCandidateConflict(code?: string) {
  if (code === 'schema_conflict' && outdated.value) {
    failure.value = 'admin.content_schema_outdated'
    return
  }
  if (code === 'version_conflict' || code === 'schema_conflict' || !code) {
    conflict.value = true
    failure.value = 'admin.content_conflict'
    return
  }
  const hints = new Map([
    ['not_ready', 'admin.content_publication_translations_need_checks'],
    ['route_conflict', 'admin.content_publication_route_conflict'],
    ['locale_unavailable', 'admin.content_language_disabled'],
    ['relation_unavailable', 'admin.content_publication_relation_unavailable'],
  ])
  failure.value = hints.get(code) ?? 'admin.content_transition_failed'
}

async function refresh() {
  const result = await readDraft(props.typeKey, props.itemId)
  if (!active) return
  if (draft.value?.publication_id !== result.draft.publication_id) reviewPublishedTranslations.value = false
  draft.value = result.draft
  metadata.value = result.metadata
  if (!activeLanguage(selectedLanguage.value) && !ownValue(result.draft.locales, selectedLanguage.value)) {
    const available = props.languages.map(language => language.code)
    const populated = new Set(Object.keys(result.draft.locales).filter(code => {
      const locale = ownValue(result.draft.locales, code)
      return locale && Object.keys(locale.values).length > 0
    }))
    const preferences = [props.uiLanguage, props.defaultLanguage, ...available, ...hiddenLanguages.value]
    selectedLanguage.value = [props.initialLanguage, ...preferences.filter(code => populated.has(code)), ...preferences]
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

function writeEdit(field: EditorField, locale: string, value: Scalar | undefined) {
  if (!draft.value || !field.editable) return
  const key = editKey(field, locale)
  if (value === savedFieldValue(draft.value, field, locale)) edits.value.delete(key)
  else edits.value.set(key, { field, locale, value })
  invalid.value.delete(field.field_id)
}

function edit(field: EditorField, locale: string, value: Scalar | undefined) {
  writeEdit(field, locale, value)
  const slug = addressField.value
  if (!locale || !draft.value?.locales || !field.editable || field.key !== 'title' || !slug?.editable) return
  if (typeof value !== 'string' || !value.trim() || value === savedFieldValue(draft.value, field, locale)) {
    edits.value.delete(editKey(slug, locale))
    return
  }
  writeEdit(slug, locale, suggestSlug(value, locale, props.slugRules, props.itemId, slug.config.max_length))
}

function clearRelation(field: EditorField, locale: string) {
  if (draft.value && field.editable && field.kind === 'relation') {
    edits.value.set(editKey(field, locale), { field, locale, value: undefined })
  }
}

async function save() {
  const source = draft.value
  const address = missingAddress.value
  if (!source || (!edits.value.size && !address) || conflict.value) return
  await run(async () => {
    const changes = [...edits.value.values()]
    if (address) changes.push(address)
    const saved = await saveDraft(props.typeKey, source, props.csrf, draftPatch(changes))
    if (!active) return
    if (publicLinks.value.length && changes.some(edit => edit.field.scope === 'shared')) reviewPublishedTranslations.value = true
    draft.value = saved
    edits.value.clear()
    invalid.value.clear()
    feedback.value = 'admin.draft_saved'
    emit('changed')
  })
  if (!active || failure.value || needsSave.value) return
  await nextTick()
  nextAction.value?.focus()
}

function missingFields(fields: EditorField[], locale: string) {
  return fields.filter(field => field.required && valueFor(field, locale) === undefined)
}

async function checkRequired(locales: string[]) {
  const locale = locales.find(code => missingFields(localeFields.value, code).length > 0)
  const missing = [...missingFields(sharedFields.value, ''), ...(locale ? missingFields(localeFields.value, locale) : [])]
  if (missing.length === 0) return true
  confirmation.value = null
  if (locale && selectedLanguage.value !== locale) {
    selectedLanguage.value = locale
    changeDraftLanguage()
  }
  invalid.value = new Set(missing.map(field => field.field_id))
  failure.value = 'admin.content_required_missing'
  await nextTick()
  const firstError = form.value?.querySelector<HTMLElement>('[aria-invalid="true"]:not([readonly]):not(:disabled)')
  if (firstError) firstError.focus()
  else {
    if (addressField.value && missing.includes(addressField.value)) failure.value = 'admin.content_publication_address_required'
    publicationHeading.value?.focus()
  }
  return false
}

async function recordReadiness(source: EditableDraft, locales: string[]) {
  let latest: DraftSummary = source
  if (!source.shared_ready_seq) latest = await sendCandidateCommand(latest, 'ready-shared')
  return locales.filter(code => !localeReady(code)).reduce<Promise<DraftSummary>>(async (previous, locale) => {
    return sendCandidateCommand(await previous, 'ready', { locale })
  }, Promise.resolve(latest))
}

async function sendCandidateCommand(source: DraftSummary, action: DraftCommand, input?: Parameters<typeof commandDraft>[4]) {
  if (!active) throw new AdminContentError(409)
  const receipt = await commandDraft(props.typeKey, source, action, props.csrf, input)
  if (active) emit('changed')
  if (receipt.item_id !== source.item_id || (action === 'publish' && !receipt.publication_id)) throw new AdminContentError(503)
  return receipt
}

async function reconcileCandidate() {
  // Earlier readiness commands may already be committed.
  if (active) await refresh().catch(() => { conflict.value = true })
}

async function applyCandidate(locales: string[], publish: boolean) {
  const source = draft.value
  if (!source || locked.value || outdated.value || !(await checkRequired(locales))) return
  await run(async () => {
    let latest: DraftSummary
    try {
      latest = await recordReadiness(source, locales)
    }
    catch (error: unknown) {
      await reconcileCandidate()
      throw error
    }
    if (publish) return publishCandidate(latest, locales)
    if (!active) return
    confirmation.value = null
    await refresh()
    feedback.value = 'admin.content_ready_saved'
  })
}

async function publishCandidate(source: DraftSummary, locales: string[]) {
  try {
    await sendCandidateCommand(source, 'publish', { intended_locales: locales })
  }
  catch (error: unknown) {
    await reconcileCandidate()
    if (!active) return
    if (!(error instanceof AdminContentError) || error.status >= 500 || error.status === 408) {
      confirmation.value = null
      conflict.value = true
      failure.value = 'admin.content_publication_unknown'
      return
    }
    if (error.code === 'not_ready' && omitsPublishedTranslations(locales)) {
      reviewPublishedTranslations.value = true
      if (publishedChecksMissing.value) confirmation.value = null
    }
    throw error
  }
  if (!active) return
  confirmation.value = null
  reviewPublishedTranslations.value = false
  feedback.value = 'admin.content_published'
  try { await refresh() }
  catch {
    if (!active) return
    conflict.value = true
    failure.value = 'admin.content_publication_reload_links'
  }
}

async function transition(action: 'adopt-schema' | 'unpublish') {
  const source = draft.value
  if (!source || locked.value) return
  const adopt = action === 'adopt-schema'
  await run(async () => {
    await sendCandidateCommand(source, action, adopt ? { schema_no: metadata.value?.current_schema_no } : undefined)
    if (!active) return
    reviewPublishedTranslations.value = adopt && publicLinks.value.length > 0
    confirmation.value = null
    await refresh()
    feedback.value = adopt ? 'admin.content_schema_adopted' : 'admin.content_unpublished'
  })
}

function publish() {
  if (locked.value || needsSave.value || publishedChecksMissing.value) return
  if (directPublishLanguage.value) return applyCandidate([directPublishLanguage.value.code], true)
  if (publishLanguages.value.length) return ask('publish')
}

async function ask(question: NonNullable<typeof confirmation.value>) {
  if (question === 'publish') {
    selectedPublications.value = publishLanguages.value.filter(({ code }) => code === selectedLanguage.value).map(({ code }) => code)
  }
  confirmation.value = question
  await nextTick()
  confirmationHeading.value?.focus()
}

async function cancelConfirmation() {
  confirmation.value = null
  await nextTick()
  if (nextAction.value) nextAction.value.focus()
  else heading.value?.focus()
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
      if (!publicationSelectionIncomplete.value) return applyCandidate(selectedPublications.value, true)
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
      <button
        v-if="draft && metadata && !assignmentsOpen" ref="assignmentsButton" type="button" class="admin-secondary"
        :disabled="locked || confirmation !== null" @click="assignmentsOpen = true"
      >{{ translate('admin.assignees') }}</button>
    </header>
    <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
    <p v-else-if="feedback"><output>{{ translate(feedback) }}</output></p>
    <p v-if="failure" role="alert" class="draft-error">{{ translate(failure) }}</p>
    <button v-if="conflict || (!draft && failure)" type="button" :disabled="busy" @click="edits.size ? ask('reload') : run(refresh)">
      {{ translate('admin.content_reload') }}
    </button>
    <section v-if="confirmation" class="publication-panel" aria-labelledby="draft-confirmation-heading">
      <h3 id="draft-confirmation-heading" ref="confirmationHeading" tabindex="-1">
        {{ translate(confirmation === 'close_assignments' ? 'admin.leave_unsaved' : `admin.content_confirm_${confirmation}`) }}
      </h3>
      <fieldset v-if="confirmation === 'publish'" :disabled="busy">
        <legend>{{ translate('admin.content_publish_languages') }}</legend>
        <p v-if="reviewPublishedTranslations">{{ translate('admin.content_publication_translations_need_checks') }}</p>
        <label v-for="language in publishLanguages" :key="language.code" class="draft-checkbox">
          <input v-model="selectedPublications" type="checkbox" :value="language.code">
          <span :lang="language.code" :dir="language.direction">{{ language.native_name }}</span>
        </label>
      </fieldset>
      <div class="draft-bar">
        <button type="button" :disabled="busy || (confirmation === 'publish' && (publicationSelectionIncomplete || conflict))" @click="confirm">
          {{ translate(confirmation === 'publish' ? 'admin.content_publish' : 'admin.content_confirm') }}
        </button>
        <button type="button" class="admin-secondary" :disabled="busy" @click="cancelConfirmation">{{ translate('admin.cancel') }}</button>
      </div>
    </section>

    <AdminAssignees
      v-if="assignmentsOpen" :csrf="csrf" :type-key="typeKey" :item-id="itemId" :translate="translate"
      :inert="confirmation !== null"
      @step-up="emit('step-up')" @session-changed="emit('session-changed')" @saved="assignmentsSaved"
      @close="assignmentsState.dirty ? ask('close_assignments') : closeAssignments()"
      @editing-state="assignmentsState = $event"
    />
    <template v-else-if="draft && metadata">
      <section class="publication-panel" aria-labelledby="draft-publication-heading" :inert="confirmation !== null">
        <div class="draft-bar">
          <h3 id="draft-publication-heading" ref="publicationHeading" tabindex="-1">{{ translate('admin.content_publication_heading') }}</h3>
          <span v-if="!publicationRefreshRequired" class="publication-state">{{ translate(publicLinks.length ? 'admin.draft_status_published' : 'admin.content_not_published') }}</span>
        </div>
        <output v-if="!publicationRefreshRequired" id="draft-publication-next">{{ translate(publicationHint) }}</output>
        <div v-if="primaryAction || (metadata.actions.unpublish && draft.publication_id)" class="draft-bar">
          <button
            v-if="primaryAction === 'save'" ref="nextAction" type="submit" form="draft-material-form" :disabled="busy || conflict"
            class="admin-domain-save" :aria-label="translate('admin.save_draft')" :title="translate('admin.save_draft')"
            aria-describedby="draft-publication-next"
          ><AdminIcon name="save" /></button>
          <button
            v-else-if="primaryAction === 'publish'" ref="nextAction" type="button" :disabled="locked"
            :aria-describedby="directPublishLanguage ? 'draft-publication-next draft-publish-language' : 'draft-publication-next'" @click="publish"
          >{{ translate('admin.content_publish') }}</button>
          <button
            v-else-if="primaryAction === 'prepare'" ref="nextAction" type="button" :disabled="locked"
            aria-describedby="draft-publication-next" @click="applyCandidate([selectedLanguage], false)"
          >{{ translate('admin.content_prepare_translation') }}</button>
          <button
            v-else-if="primaryAction === 'ready-shared'" ref="nextAction" type="button" :disabled="locked"
            aria-describedby="draft-publication-next" @click="applyCandidate([], false)"
          >{{ translate('admin.content_mark_shared') }}</button>
          <button
            v-else-if="primaryAction === 'adopt-schema'" ref="nextAction" type="button" :disabled="locked"
            aria-describedby="draft-publication-next" @click="transition('adopt-schema')"
          >{{ translate('admin.content_adopt_schema') }}</button>
          <span v-if="primaryAction === 'publish' && directPublishLanguage" id="draft-publish-language" :lang="directPublishLanguage.code" :dir="directPublishLanguage.direction">
            {{ directPublishLanguage.native_name }}
          </span>
          <button
            v-if="metadata.actions.unpublish && draft.publication_id" type="button" class="admin-secondary"
            :disabled="locked" @click="ask('unpublish')"
          >{{ translate('admin.content_unpublish') }}</button>
        </div>
        <div v-if="publicLinks.length && !publicationRefreshRequired && !busy" class="publication-links">
          <p id="draft-links-caption">{{ translate('admin.content_links_new_tab') }}</p>
          <ul aria-labelledby="draft-links-caption">
            <li v-for="item in publicLinks" :key="item.language">
              <a :href="item.canonical_url" :lang="item.language" :hreflang="item.language" target="_blank" rel="noopener">
                {{ languageName(item.language) }}
              </a>
            </li>
          </ul>
        </div>
      </section>

      <form id="draft-material-form" ref="form" @submit.prevent="save">
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
          <p v-if="localeReady(selectedLanguage)">{{ translate('admin.content_locale_ready') }}</p>
          <template v-if="selectedLanguage">
            <DraftFieldControl
              v-for="field in visibleLocaleFields" :key="`${field.field_id}/${selectedLanguage}`" :field="field" v-bind="description(field)"
              :value="valueFor(field, selectedLanguage)" :invalid="invalid.has(field.field_id)" :translate="translate"
              @change="edit(field, selectedLanguage, $event)" @clear-relation="clearRelation(field, selectedLanguage)"
            />
          </template>
        </fieldset>
      </form>
    </template>
  </section>
</template>

<style scoped>
.draft-editor, form, fieldset {
  display: grid;
  gap: 1rem;
  min-width: 0;
}

.draft-editor :is(a, p, output) {
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

.draft-bar h2 {
  margin: 0;
  margin-inline-end: auto;
}

.publication-panel {
  display: grid;
  gap: .65rem;
  padding: .85rem;
  border: 1px solid var(--admin-border);
  border-radius: .5rem;
}

.publication-panel :is(h3, p) {
  margin: 0;
}

.publication-state {
  font-size: .875rem;
  color: var(--admin-muted);
}

.publication-links ul {
  display: flex;
  flex-wrap: wrap;
  gap: .85rem;
  margin-block: .35rem 0;
  padding: 0;
  list-style: none;
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
