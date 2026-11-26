<script setup lang="ts">
import { AdminContentError } from '~/utils/admin-request'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { components } from '@stepolonia/sdk'
import { usePublicApi } from '~/composables/usePublicApi'
import { createDraft, listDrafts } from '~/utils/admin-content'
import type { CollectionChoice, DraftSummary } from '~/utils/admin-content'
import { ownValue } from '~/utils/draft-edit-buffer'
import type { SlugRule } from '~/utils/slug-suggestion'

const props = defineProps<{
  csrf: string
  collection: CollectionChoice
  collectionLabel: string
  uiLanguage: string
  defaultLanguage: string
  slugRules: SlugRule[]
  translate: (key: string) => string
}>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [], 'editing-state': [state: { dirty: boolean, busy: boolean }] }>()
const { client, request } = usePublicApi()
const languages = ref<components['schemas']['Language'][]>([])
const drafts = ref<DraftSummary[]>([])
const selectedType = computed(() => props.collection.type_key)
const selectedItem = ref('')
const initialLanguage = ref('')
const lastLanguages = new Map<string, string>()
const pageOffsets = new Map<string, number>()
let draftChanged = false
const unknownPublications = ref(new Set<string>())
const openedType = ref('')
const draftOffset = ref<number | null>(null)
const opener = ref<HTMLElement | null>(null)
const heading = ref<HTMLElement | null>(null)
const busy = ref(false)
const creating = ref(false)
const editorState = ref({ dirty: false, busy: false })
const loaded = ref(false)
const failure = ref('')
const active = ref(true)

watch([creating, editorState], ([creatingDraft, state]) => {
  emit('editing-state', { dirty: state.dirty, busy: creatingDraft || state.busy })
})

function titleLanguage(item: DraftSummary) {
  const otherLanguages = Object.keys(item.titles ?? {}).sort((left, right) => left.localeCompare(right, 'en'))
  return [props.uiLanguage, props.defaultLanguage, ...otherLanguages]
    .find(code => ownValue(item.titles, code)) ?? ''
}

function nativeName(code: string) {
  return languages.value.find(language => language.code === code)?.native_name ?? code
}

async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  try { await work() }
  catch (error: unknown) { if (active.value) showError(error) }
  finally { busy.value = false }
}

function showError(error: unknown) {
  failure.value = 'admin.unavailable'
  if (!(error instanceof AdminContentError)) return
  if (error.code === 'step_up_required') {
    failure.value = ''
    emit('step-up')
  }
  else if (error.status === 401 || error.code === 'csrf_failed') {
    failure.value = 'admin.changed'
    emit('session-changed')
  }
  else if (error.status === 403 || error.status === 404) {
    failure.value = 'admin.access_denied'
  }
  else if (error.status === 409) {
    failure.value = 'admin.content_conflict'
  }
}

async function loadWorkspace() {
  await run(async () => {
    const activeLanguages = await request(() => client.listLanguages())
    if (!activeLanguages.data) throw new AdminContentError(activeLanguages.response.status)
    if (!active.value) return
    languages.value = activeLanguages.data
    await loadDraftPage()
  })
}

async function loadDraftPage(offset = 0) {
  const type = selectedType.value
  const page = await listDrafts(type, offset)
  if (!active.value || selectedType.value !== type) return
  if (offset === 0) pageOffsets.clear()
  for (const item of page.items) {
    pageOffsets.set(item.item_id, offset)
    unknownPublications.value.delete(item.item_id)
  }
  drafts.value = offset === 0 ? page.items : [...drafts.value, ...page.items]
  draftOffset.value = page.next_offset
  loaded.value = true
}

function openDraft(itemID: string, target: EventTarget | null, language = '') {
  if (selectedItem.value) return
  draftChanged = false
  openedType.value = selectedType.value
  initialLanguage.value = language || lastLanguages.get(itemID) || ''
  selectedItem.value = itemID
  opener.value = target instanceof HTMLElement ? target : null
}

function rememberDraftLanguage(language: string) {
  lastLanguages.set(selectedItem.value, language)
}

function updateDraftPublication(locales: string[] | null) {
  const itemID = selectedItem.value
  if (!itemID) return
  if (locales === null) {
    unknownPublications.value.add(itemID)
    draftChanged = true
    return
  }
  unknownPublications.value.delete(itemID)
  drafts.value = drafts.value.map(item => item.item_id === itemID ? { ...item, published_locales: locales } : item)
}

async function closeDraft() {
  const closedItem = selectedItem.value
  const focusAtClose = document.activeElement
  selectedItem.value = ''
  openedType.value = ''
  editorState.value = { dirty: false, busy: false }
  if (draftChanged) await run(async () => {
    const offset = pageOffsets.get(closedItem) ?? 0
    const page = await listDrafts(selectedType.value, offset)
    if (!active.value) return
    unknownPublications.value.delete(closedItem)
    const fresh = page.items.find(item => item.item_id === closedItem)
    if (fresh) drafts.value = drafts.value.map(item => item.item_id === closedItem ? fresh : item)
    else {
      // A concurrent list change can move or hide the row. Keep the other loaded pages.
      drafts.value = drafts.value.filter(item => item.item_id !== closedItem)
      pageOffsets.delete(closedItem)
    }
  })
  await nextTick()
  if (!active.value || selectedItem.value || (document.activeElement !== document.body && document.activeElement !== focusAtClose)) return
  if (opener.value?.isConnected) opener.value.focus()
  else heading.value?.focus()
}

async function create(event: Event) {
  const collection = props.collection
  const target = event.currentTarget
  if (!collection?.can_create || selectedItem.value || busy.value) return
  creating.value = true
  try {
    await run(async () => {
      const receipt = await createDraft(collection.type_key, collection.schema_no, props.csrf)
      if (!active.value) return
      openDraft(receipt.item_id, target)
      await loadDraftPage()
    })
  } finally {
    creating.value = false
  }
}

onMounted(() => { void loadWorkspace() })
onBeforeUnmount(() => { active.value = false })
</script>

<template>
  <section :class="['content-workspace', { 'has-draft': selectedItem }]" aria-labelledby="content-workspace-title">
    <div class="workspace-list" :aria-busy="busy">
      <h2 id="content-workspace-title" ref="heading" tabindex="-1">{{ collectionLabel }}</h2>
      <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
      <p v-if="failure" role="alert">{{ translate(failure) }}</p>
      <button v-if="failure && !selectedItem" type="button" :disabled="busy" @click="loadWorkspace">{{ translate('admin.retry') }}</button>
      <template v-if="loaded">
        <button v-if="collection.can_create" type="button" :disabled="busy || Boolean(selectedItem)" @click="create">
          {{ translate('admin.content_create') }}
        </button>
        <p v-if="loaded && !busy && !failure && !drafts.length">{{ translate('admin.content_no_drafts') }}</p>
        <ul v-if="drafts.length" class="draft-list">
          <li v-for="item in drafts" :key="item.item_id" :data-item-id="item.item_id">
            <button
              type="button" class="admin-secondary" :disabled="busy || Boolean(selectedItem)"
              @click="openDraft(item.item_id, $event.currentTarget, titleLanguage(item))"
            >
              <span class="draft-title" :lang="titleLanguage(item) || undefined" dir="auto">
                {{ ownValue(item.titles, titleLanguage(item)) ?? translate('admin.draft_untitled') }}
              </span>
              <span class="draft-summary">
                <span v-if="titleLanguage(item)" :lang="titleLanguage(item)">{{ nativeName(titleLanguage(item)) }}</span>
                <template v-if="!unknownPublications.has(item.item_id)">
                  <span v-if="item.published_locales?.length">
                    {{ translate('admin.draft_status_published') }}
                    <span v-for="(code, index) in item.published_locales" :key="code"><span v-if="index">, </span><span :lang="code">{{ nativeName(code) }}</span></span>
                  </span>
                  <span v-else>{{ translate('admin.draft_status_unpublished') }}</span>
                </template>
              </span>
            </button>
          </li>
        </ul>
        <button v-if="draftOffset !== null" type="button" class="admin-secondary" :disabled="busy || Boolean(selectedItem)" @click="run(() => loadDraftPage(draftOffset ?? 0))">
          {{ translate('admin.content_more_drafts') }}
        </button>
      </template>
    </div>
    <AdminDraftEditor
      v-if="selectedItem" :key="selectedItem" :type-key="openedType" :item-id="selectedItem" :csrf="csrf"
      :translate="translate" :ui-language="uiLanguage" :languages="languages" :slug-rules="slugRules"
      :initial-language="initialLanguage" :default-language="defaultLanguage"
      @language-changed="rememberDraftLanguage" @changed="draftChanged = true"
      @published-languages="updateDraftPublication"
      @editing-state="editorState = $event"
      @close="closeDraft" @step-up="emit('step-up')" @session-changed="emit('session-changed')"
    />
  </section>
</template>

<style scoped>
.content-workspace, .workspace-list {
  display: grid;
  min-width: 0;
  gap: .75rem;
}

.content-workspace :deep(button) {
  min-height: 44px;
  min-width: 0;
  max-width: 100%;
  padding: .55rem .8rem;
  border: 1px solid var(--admin-action);
  border-radius: .4rem;
  background: var(--admin-action);
  color: var(--admin-action-text);
  font: inherit;
  font-weight: 700;
  cursor: pointer;
}

.content-workspace :deep(.admin-secondary) {
  background: var(--admin-card);
  color: var(--admin-text);
}

.content-workspace :deep(button:disabled) {
  opacity: .65;
  cursor: default;
}

.content-workspace :deep(:focus-visible) {
  outline: 3px solid var(--admin-focus);
  outline-offset: 3px;
}

.workspace-list select {
  min-width: 0;
  min-height: 44px;
  padding: .5rem;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
}

.draft-list {
  display: grid;
  list-style: none;
  gap: .45rem;
  margin: 0;
  padding: 0;
}

.draft-list button {
  width: 100%;
  overflow-wrap: anywhere;
  text-align: start;
}

.draft-title {
  display: block;
}

.draft-summary {
  display: flex;
  flex-wrap: wrap;
  column-gap: .85rem;
  color: var(--admin-muted);
  font-size: .875rem;
  font-weight: 400;
}

@media (min-width: 70rem) {
  .content-workspace.has-draft {
    grid-template-columns: minmax(13rem, 20rem) minmax(0, 1fr);
    align-items: start;
    gap: 1.5rem;
  }
}

@media (width < 70rem) {
  .content-workspace.has-draft > .workspace-list {
    display: none;
  }
}
</style>
