<script setup lang="ts">
import { AdminContentError } from '~/utils/admin-request'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { components } from '@stepolonia/sdk'
import { usePublicApi } from '~/composables/usePublicApi'
import { createDraft, listCollections, listDrafts } from '~/utils/admin-content'
import type { CollectionChoice, DraftSummary } from '~/utils/admin-content'
import { ownValue } from '~/utils/draft-edit-buffer'

const props = defineProps<{
  csrf: string
  uiLanguage: string
  defaultLanguage: string
  translate: (key: string) => string
}>()
const emit = defineEmits<{ close: [], 'step-up': [], 'session-changed': [], 'editing-state': [state: { dirty: boolean, busy: boolean }] }>()
const { client, request } = usePublicApi()
const collections = ref<CollectionChoice[]>([])
const languages = ref<components['schemas']['Language'][]>([])
const drafts = ref<DraftSummary[]>([])
const selectedType = ref('')
const selectedItem = ref('')
const initialLanguage = ref('')
const lastLanguages = new Map<string, string>()
const pageOffsets = new Map<string, number>()
let draftChanged = false
const openedType = ref('')
const collectionOffset = ref<number | null>(null)
const draftOffset = ref<number | null>(null)
const opener = ref<HTMLElement | null>(null)
const heading = ref<HTMLElement | null>(null)
const busy = ref(false)
const creating = ref(false)
const editorState = ref({ dirty: false, busy: false })
const loaded = ref(false)
const failure = ref('')
const active = ref(true)

const selectedCollection = computed(() => collections.value.find(item => item.type_key === selectedType.value))
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

function collectionLabel(key: string) {
  if (key === 'article') return props.translate('articles')
  if (key === 'event') return props.translate('events')
  return key
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
    failure.value = 'admin.step_up_required'
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
    const [page, activeLanguages] = await Promise.all([listCollections(), request(() => client.listLanguages())])
    if (!activeLanguages.data) throw new AdminContentError(activeLanguages.response.status)
    if (!active.value) return
    collections.value = page.items
    collectionOffset.value = page.next_offset
    languages.value = activeLanguages.data
    selectedType.value = page.items[0]?.type_key ?? ''
    if (selectedType.value) await loadDraftPage()
    else loaded.value = true
  })
}

async function moreCollections() {
  const offset = collectionOffset.value
  if (offset === null) return
  await run(async () => {
    const page = await listCollections(offset)
    if (!active.value) return
    collections.value = [...collections.value, ...page.items]
    collectionOffset.value = page.next_offset
  })
}

async function loadDraftPage(offset = 0) {
  const type = selectedType.value
  const page = await listDrafts(type, offset)
  if (!active.value || selectedType.value !== type) return
  if (offset === 0) pageOffsets.clear()
  for (const item of page.items) pageOffsets.set(item.item_id, offset)
  drafts.value = offset === 0 ? page.items : [...drafts.value, ...page.items]
  draftOffset.value = page.next_offset
  loaded.value = true
}

function changeCollection() {
  drafts.value = []
  draftOffset.value = null
  return run(loadDraftPage)
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

async function closeDraft() {
  const closedItem = selectedItem.value
  selectedItem.value = ''
  openedType.value = ''
  editorState.value = { dirty: false, busy: false }
  if (draftChanged) await run(async () => {
    const offset = pageOffsets.get(closedItem) ?? 0
    const page = await listDrafts(selectedType.value, offset)
    if (!active.value) return
    const fresh = page.items.find(item => item.item_id === closedItem)
    if (fresh) drafts.value = drafts.value.map(item => item.item_id === closedItem ? fresh : item)
    else {
      // A concurrent list change can move or hide the row. Keep the other loaded pages.
      drafts.value = drafts.value.filter(item => item.item_id !== closedItem)
      pageOffsets.delete(closedItem)
    }
  })
  await nextTick()
  if (opener.value?.isConnected) opener.value.focus()
  else heading.value?.focus()
}

async function create(event: Event) {
  const collection = selectedCollection.value
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
      <header class="content-bar">
        <h2 id="content-workspace-title" ref="heading" tabindex="-1">{{ translate('admin.content_collections') }}</h2>
        <button v-if="!selectedItem" type="button" class="admin-secondary" :disabled="busy" @click="emit('close')">
          {{ translate('admin.close') }}
        </button>
      </header>
      <p v-if="busy"><output>{{ translate('admin.working') }}</output></p>
      <p v-if="failure" role="alert">{{ translate(failure) }}</p>
      <button v-if="failure && !selectedItem" type="button" :disabled="busy" @click="loadWorkspace">{{ translate('admin.retry') }}</button>
      <p v-if="loaded && !collections.length">{{ translate('admin.content_no_collections') }}</p>
      <template v-if="collections.length">
        <label for="collection-choice">{{ translate('admin.content_collection') }}</label>
        <select id="collection-choice" v-model="selectedType" :disabled="busy || Boolean(selectedItem)" @change="changeCollection">
          <option v-for="entry in collections" :key="entry.type_key" :value="entry.type_key">{{ collectionLabel(entry.type_key) }}</option>
        </select>
        <button
          v-if="collectionOffset !== null" type="button" class="admin-secondary" :disabled="busy || Boolean(selectedItem)"
          @click="moreCollections"
        >{{ translate('admin.content_more_collections') }}</button>
        <button v-if="selectedCollection?.can_create" type="button" :disabled="busy || Boolean(selectedItem)" @click="create">
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
                <span v-if="item.published_locales?.length">
                  {{ translate('admin.draft_status_published') }}
                  <span v-for="(code, index) in item.published_locales" :key="code"><span v-if="index">, </span><span :lang="code">{{ nativeName(code) }}</span></span>
                </span>
                <span v-else>{{ translate('admin.draft_status_unpublished') }}</span>
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
      :translate="translate" :ui-language="uiLanguage" :languages="languages"
      :initial-language="initialLanguage" :default-language="defaultLanguage"
      @language-changed="rememberDraftLanguage" @changed="draftChanged = true"
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

.content-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: .65rem;
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
    grid-template-columns: minmax(13rem, 1fr) minmax(0, 2fr);
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
