<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { components } from '@stepolonia/sdk'
import { usePublicApi } from '~/composables/usePublicApi'
import { AdminContentError, createDraft, listCollections, listDrafts } from '~/utils/admin-content'
import type { CollectionChoice, DraftSummary } from '~/utils/admin-content'

const props = defineProps<{
  csrf: string
  uiLanguage: string
  translate: (key: string) => string
}>()
const emit = defineEmits<{ close: [], 'step-up': [], 'session-changed': [] }>()
const { client, request } = usePublicApi()
const collections = ref<CollectionChoice[]>([])
const languages = ref<components['schemas']['Language'][]>([])
const drafts = ref<DraftSummary[]>([])
const selectedType = ref('')
const selectedItem = ref('')
const openedType = ref('')
const collectionOffset = ref<number | null>(null)
const draftOffset = ref<number | null>(null)
const opener = ref<HTMLElement | null>(null)
const heading = ref<HTMLElement | null>(null)
const busy = ref(false)
const loaded = ref(false)
const failure = ref('')
let active = true

const selectedCollection = computed(() => collections.value.find(item => item.type_key === selectedType.value))

async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  try { await work() }
  catch (error: unknown) { if (active) showError(error) }
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
    if (!active) return
    collections.value = page.items
    collectionOffset.value = page.next_offset
    languages.value = activeLanguages.data
    selectedType.value = page.items[0]?.type_key ?? ''
    if (selectedType.value) await loadDraftPage()
    if (active) loaded.value = true
  })
}

async function moreCollections() {
  const offset = collectionOffset.value
  if (offset === null) return
  await run(async () => {
    const page = await listCollections(offset)
    if (!active) return
    collections.value = [...collections.value, ...page.items]
    collectionOffset.value = page.next_offset
  })
}

async function loadDraftPage(offset = 0) {
  const type = selectedType.value
  const page = await listDrafts(type, offset)
  if (!active || selectedType.value !== type) return
  drafts.value = offset === 0 ? page.items : [...drafts.value, ...page.items]
  draftOffset.value = page.next_offset
}

function changeCollection() {
  drafts.value = []
  draftOffset.value = null
  return run(loadDraftPage)
}

function openDraft(itemID: string, target: EventTarget | null) {
  if (selectedItem.value) return
  openedType.value = selectedType.value
  selectedItem.value = itemID
  opener.value = target instanceof HTMLElement ? target : null
}

async function closeDraft() {
  selectedItem.value = ''
  openedType.value = ''
  await nextTick()
  if (opener.value?.isConnected) opener.value.focus()
  else heading.value?.focus()
}

async function create(event: Event) {
  const collection = selectedCollection.value
  const target = event.currentTarget
  if (!collection?.can_create || selectedItem.value) return
  await run(async () => {
    const receipt = await createDraft(collection.type_key, collection.schema_no, props.csrf)
    if (!active) return
    openDraft(receipt.item_id, target)
    await loadDraftPage()
  })
}

onMounted(() => { void loadWorkspace() })
onBeforeUnmount(() => { active = false })
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
      <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
      <p v-if="failure" role="alert">{{ translate(failure) }}</p>
      <button v-if="failure && !selectedItem" type="button" :disabled="busy" @click="loadWorkspace">{{ translate('admin.retry') }}</button>
      <p v-if="loaded && !collections.length">{{ translate('admin.content_no_collections') }}</p>
      <template v-if="collections.length">
        <label for="collection-choice">{{ translate('admin.content_collection') }}</label>
        <select id="collection-choice" v-model="selectedType" :disabled="busy || Boolean(selectedItem)" @change="changeCollection">
          <option v-for="entry in collections" :key="entry.type_key" :value="entry.type_key">{{ entry.type_key }}</option>
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
          <li v-for="item in drafts" :key="item.item_id">
            <button
              type="button" class="admin-secondary" :disabled="busy || Boolean(selectedItem)"
              @click="openDraft(item.item_id, $event.currentTarget)"
            >{{ item.item_id }}</button>
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
