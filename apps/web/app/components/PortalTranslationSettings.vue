<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { PortalLanguage } from '~/utils/admin-site-settings'

const props = defineProps<{
  languages: PortalLanguage[]
  language: string
  referenceLanguage: string
  keys: string[]
  messages?: Record<string, string>
  reference?: Record<string, string>
  uiLanguage: string
  uiMessages: Record<string, string>
  disabledCatalogs: string[]
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  'update:language': [code: string]
  'update:message': [key: string, value: string]
  'add-catalog': []
  'remove-catalog': [code: string]
}>()

const groups = ['all', 'admin.settings_public_texts', 'admin.content', 'admin.workspace_structure', 'admin.access', 'admin.settings', 'admin.name']
const query = ref('')
const group = ref('all')
const missingOnly = ref(false)
const selected = ref('')
const removal = ref('')
const input = ref<HTMLTextAreaElement>()
const search = ref<HTMLInputElement>()
const removalHeading = ref<HTMLElement>()
const languageInput = ref<HTMLSelectElement>()
const pageSize = 8
const offset = ref(0)
const messages = computed(() => new Map(Object.entries(props.messages ?? {})))
const reference = computed(() => new Map(Object.entries(props.reference ?? {})))
const activeLanguage = computed(() => props.languages.find(language => language.code === props.language))
const referenceLanguage = computed(() => props.languages.find(language => language.code === props.referenceLanguage))
const filtered = computed(() => {
  const term = query.value.trim().toLowerCase()
  return props.keys.filter((key) => {
    const value = messages.value.get(key) ?? ''
    return (group.value === 'all' || domain(key) === group.value) &&
      (!missingOnly.value || !value.trim()) &&
      (!term || [key, value, reference.value.get(key), props.uiMessages[key]]
        .some(text => text?.toLowerCase().includes(term)))
  })
})
const visible = computed(() => filtered.value.slice(offset.value, offset.value + pageSize))
watch([query, group, missingOnly, () => props.language], () => { offset.value = 0 })
watch(() => props.language, () => {
  selected.value = ''
  removal.value = ''
})
watch(filtered, (keys) => {
  if (keys.length <= offset.value) offset.value = Math.max(0, Math.ceil(keys.length / pageSize - 1) * pageSize)
})

function domain(key: string) {
  if (!key.startsWith('admin.')) return 'admin.settings_public_texts'
  if (/^admin\.(content|draft|field)_/u.test(key)) return 'admin.content'
  if (key.startsWith('admin.registry_')) return 'admin.workspace_structure'
  if (/^admin\.(access|users|roles|audit|assignees|account)_/u.test(key)) return 'admin.access'
  if (/^admin\.(settings|portal|catalog)_/u.test(key)) return 'admin.settings'
  return 'admin.name'
}

function messageName(key: string) {
  return props.uiMessages[key] || reference.value.get(key) || messages.value.get(key) || key
}

function messageLanguage(key: string) {
  if (props.uiMessages[key]) return props.uiLanguage
  if (reference.value.get(key)) return props.referenceLanguage
  return props.language
}

async function edit(key: string) {
  selected.value = key
  await nextTick()
  input.value?.focus()
}

async function closeEditor() {
  selected.value = ''
  await nextTick()
  search.value?.focus()
}

async function showMissing() {
  query.value = ''
  group.value = 'all'
  missingOnly.value = true
  offset.value = 0
  removal.value = ''
  await nextTick()
  if (selected.value && filtered.value.includes(selected.value)) {
    input.value?.focus()
    return
  }
  selected.value = ''
  const field = search.value ?? languageInput.value
  field?.focus()
}

async function confirmRemoval(code: string) {
  removal.value = code
  await nextTick()
  removalHeading.value?.focus()
}

async function closeRemoval(remove: boolean) {
  const code = removal.value
  removal.value = ''
  if (remove) {
    if (code === props.language) selected.value = ''
    emit('remove-catalog', code)
  }
  await nextTick()
  languageInput.value?.focus()
}

defineExpose({ showMissing })
</script>

<template>
  <div class="catalog-workspace">
    <div class="catalog-controls">
      <label for="portal-catalog-language">{{ translate('admin.settings_translation_language') }}</label>
      <select
        id="portal-catalog-language" ref="languageInput" :value="language"
        @change="emit('update:language', ($event.target as HTMLSelectElement).value)">
        <option v-for="item in languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
          {{ item.native_name }}
        </option>
      </select>
      <button v-if="activeLanguage && !props.messages" type="button" @click="emit('add-catalog')">
        {{ translate('admin.add_catalog') }}
      </button>
      <button v-if="activeLanguage && props.messages && language !== props.referenceLanguage" type="button" @click="confirmRemoval(language)">
        {{ translate('admin.remove_catalog') }}
      </button>
      <button v-for="code in disabledCatalogs" :key="code" type="button" @click="confirmRemoval(code)">
        {{ translate('admin.remove_catalog') }} ({{ code }})
      </button>
    </div>
    <section v-if="removal" class="catalog-editor" aria-labelledby="catalog-removal-heading">
      <h3 id="catalog-removal-heading" ref="removalHeading" tabindex="-1">{{ translate('admin.settings_confirm_remove_catalog') }}</h3>
      <p :lang="removal">{{ languages.find(item => item.code === removal)?.native_name ?? removal }}</p>
      <div class="catalog-pagination">
        <button type="button" @click="closeRemoval(false)">{{ translate('admin.cancel') }}</button>
        <button type="button" @click="closeRemoval(true)">{{ translate('admin.remove_catalog') }}</button>
      </div>
    </section>
    <template v-if="props.messages">
      <div class="catalog-controls catalog-filters">
        <div>
        <label for="portal-message-search">{{ translate('admin.settings_search_messages') }}</label>
        <input
          id="portal-message-search" ref="search" v-model="query" type="search"
          @input="selected = ''" @keydown.enter.prevent
        >
        </div>
        <div>
        <label for="portal-message-group">{{ translate('admin.settings_message_group') }}</label>
        <select id="portal-message-group" v-model="group" @change="selected = ''">
          <option v-for="item in groups" :key="item" :value="item">{{ translate(item) }}</option>
        </select>
        </div>
        <label class="catalog-filter">
          <input v-model="missingOnly" type="checkbox" @change="selected = ''">
          {{ translate('admin.settings_missing_only') }}
        </label>
      </div>
      <div class="catalog-browser" :class="{ 'catalog-browser--editing': selected }">
        <div class="catalog-result-set">
          <output v-if="!visible.length">{{ translate('admin.settings_no_messages') }}</output>
          <ul v-else class="catalog-results" :aria-label="translate('admin.settings_select_message')">
            <li v-for="key in visible" :key="key">
              <button type="button" :aria-pressed="selected === key" @click="edit(key)">
                <span :lang="messageLanguage(key)" dir="auto">{{ messageName(key) }}</span>
                <small>{{ key }}</small>
              </button>
            </li>
          </ul>
          <div v-if="offset || filtered.length > pageSize" class="catalog-pagination">
            <button type="button" :disabled="!offset" @click="offset -= pageSize">{{ translate('previous') }}</button>
            <button type="button" :disabled="offset + pageSize >= filtered.length" @click="offset += pageSize">{{ translate('next') }}</button>
          </div>
        </div>
        <div v-if="selected" class="catalog-editor">
          <button type="button" class="catalog-back" @click="closeEditor">{{ translate('admin.settings_back_to_texts') }}</button>
          <label for="portal-message-value">{{ translate('admin.settings_message_value') }}</label>
          <textarea
            id="portal-message-value" ref="input" :value="messages.get(selected) ?? ''"
            :lang="activeLanguage?.code" :dir="activeLanguage?.direction" rows="4" maxlength="4000"
            @input="emit('update:message', selected, ($event.target as HTMLTextAreaElement).value)"
          />
          <template v-if="reference.get(selected)">
            <p class="catalog-reference">
              <strong>{{ translate('admin.settings_reference_text') }}</strong>
              <span :lang="referenceLanguage?.code" :dir="referenceLanguage?.direction">{{ referenceLanguage?.native_name }}</span>
            </p>
            <p :lang="referenceLanguage?.code" dir="auto">{{ reference.get(selected) }}</p>
          </template>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.catalog-workspace, .catalog-controls, .catalog-filters > div, .catalog-editor, .catalog-results, .catalog-browser, .catalog-result-set {
  display: grid;
  gap: .5rem;
  min-inline-size: 0;
}

.catalog-browser {
  align-items: start;
}

.catalog-workspace button {
  box-sizing: border-box;
  min-block-size: 44px;
  padding: .5rem .65rem;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
  overflow-wrap: anywhere;
}

.catalog-workspace button:not(:disabled):hover {
  background: var(--admin-bg);
}

.catalog-workspace button:disabled {
  color: var(--admin-muted);
}

.catalog-results {
  list-style: none;
  padding: 0;
  margin: 0;
}

.catalog-results button {
  display: grid;
  width: 100%;
  text-align: start;
  overflow-wrap: anywhere;
}

.catalog-results button[aria-pressed="true"] {
  border-color: var(--admin-action);
  box-shadow: inset .2rem 0 var(--admin-action);
  background: var(--admin-bg);
}

.catalog-results small, .catalog-reference { color: var(--admin-muted); }
.catalog-editor h3 { margin: 0; overflow-wrap: anywhere; }
.catalog-editor p { margin: 0; overflow-wrap: anywhere; }
.catalog-editor textarea { resize: vertical; }
.catalog-reference { display: flex; flex-wrap: wrap; gap: .35rem; }
.catalog-pagination { display: flex; flex-wrap: wrap; gap: .5rem; }
.catalog-filter { display: flex; align-items: center; gap: .5rem; }
.catalog-filter input { width: auto; margin: 0; }
.catalog-back { justify-self: start; }

@media (min-width: 68rem) {
  .catalog-filters { grid-template-columns: minmax(0, 2fr) minmax(0, 1fr); }
  .catalog-filter { grid-column: 1 / -1; }
  .catalog-browser--editing { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; }
}

@media (max-width: 43.99rem) {
  .catalog-browser--editing .catalog-result-set { display: none; }
}
</style>
