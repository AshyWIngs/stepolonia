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

const groupLabels = {
  all: 'admin.settings_all_texts',
  public: 'admin.settings_public_texts',
  content: 'admin.settings_content_texts',
  structure: 'admin.settings_structure_texts',
  access: 'admin.settings_access_texts',
  settings: 'admin.settings_settings_texts',
  common: 'admin.settings_admin_texts',
}
const settingsControls = new Set([
  'admin.tagline', 'admin.description', 'admin.notice', 'admin.accent', 'admin.default_language', 'admin.fallback_language',
  'admin.localization', 'admin.interface_texts', 'admin.save_draft', 'admin.draft_saved', 'admin.publish_draft', 'admin.confirm_publish',
  'admin.published', 'admin.reload_settings', 'admin.add_catalog', 'admin.remove_catalog',
])
type CatalogGroup = keyof typeof groupLabels
const query = ref('')
const group = ref<CatalogGroup>('all')
const missingOnly = ref(false)
const selected = ref('')
const input = ref<HTMLTextAreaElement>()
const search = ref<HTMLInputElement>()
const results = ref<HTMLElement>()
const languageInput = ref<HTMLSelectElement>()
const pageSize = 16
const offset = ref(0)
const messages = computed(() => new Map(Object.entries(props.messages ?? {})))
const reference = computed(() => new Map(Object.entries(props.reference ?? {})))
const activeLanguage = computed(() => props.languages.find(language => language.code === props.language))
const referenceLanguage = computed(() => props.languages.find(language => language.code === props.referenceLanguage))
const term = computed(() => query.value.trim().toLowerCase())
const filtered = computed(() => props.keys.filter((key) => {
  const value = messages.value.get(key) ?? ''
  return (group.value === 'all' || domain(key) === group.value) &&
    (!missingOnly.value || !value.trim()) &&
    (!term.value || [key, value, reference.value.get(key), props.uiMessages[key]]
      .some(text => text?.toLowerCase().includes(term.value)))
}))
const visible = computed(() => filtered.value.slice(offset.value, offset.value + pageSize))
const duplicateContexts = computed(() => {
  const byName = Map.groupBy(filtered.value, messageName)
  const contexts = new Map<string, string>()
  for (const keys of byName.values()) {
    if (keys.length < 2) continue
    for (const [label, matchingKeys] of Map.groupBy(keys, messageContext)) {
      for (const key of matchingKeys) contexts.set(key, matchingKeys.length === 1 ? label : key)
    }
  }
  return contexts
})
const selectedReference = computed(() => props.language === props.referenceLanguage ? '' : reference.value.get(selected.value))
watch([query, group, missingOnly, () => props.language], () => { offset.value = 0 })
watch(() => props.language, () => {
  selected.value = ''
})
watch(filtered, (keys) => {
  if (keys.length <= offset.value) offset.value = Math.max(0, Math.ceil(keys.length / pageSize - 1) * pageSize)
})

function domain(key: string): CatalogGroup {
  if (!key.startsWith('admin.')) return 'public'
  if (settingsControls.has(key) || /^admin\.(settings|portal|catalog|slug_rules)(?:_|$)/u.test(key)) return 'settings'
  if (/^admin\.(content|draft|field)(?:_|$)/u.test(key)) return 'content'
  if (key === 'admin.workspace_structure' || /^admin\.registry(?:_|$)/u.test(key)) return 'structure'
  if (/^admin\.(access|users|roles|role_rule|permission|audit|assignees|security|session_policy)(?:[_.]|$)/u.test(key)) return 'access'
  return 'common'
}

function messageName(key: string) {
  return props.uiMessages[key] || reference.value.get(key) || messages.value.get(key) || key
}

function messageContext(key: string) {
  if (/^admin\.users(?:_|$)/u.test(key)) return props.translate('admin.users')
  if (/^admin\.(roles|role_rule)(?:_|$)/u.test(key)) return props.translate('admin.roles')
  return props.translate(groupLabels[domain(key)])
}

function messageLanguage(key: string) {
  if (props.uiMessages[key]) return props.uiLanguage
  if (reference.value.get(key)) return props.referenceLanguage
  return props.language
}

function translationPreview(key: string) {
  const translation = messages.value.get(key)
  return translation && translation !== messageName(key) ? translation : ''
}

async function edit(key: string) {
  selected.value = key
  await nextTick()
  input.value?.focus()
}

async function closeEditor() {
  const opener = results.value?.querySelector<HTMLElement>('button[aria-pressed="true"]')
  selected.value = ''
  await nextTick()
  const destination = opener?.isConnected ? opener : search.value
  destination?.focus()
}

async function showMissing() {
  query.value = ''
  group.value = 'all'
  missingOnly.value = true
  offset.value = 0
  await nextTick()
  if (selected.value && filtered.value.includes(selected.value)) {
    input.value?.focus()
    return
  }
  selected.value = ''
  await nextTick()
  const field = search.value ?? languageInput.value
  field?.focus()
}

async function removeCatalog(code: string) {
  if (code === props.language) selected.value = ''
  emit('remove-catalog', code)
  await nextTick()
  languageInput.value?.focus()
}

defineExpose({ showMissing })
</script>

<template>
  <div class="catalog-workspace" :class="{ 'catalog-workspace--editing': selected }">
    <div class="catalog-toolbar">
      <div v-if="props.messages" class="catalog-control catalog-search">
        <label for="portal-message-search">{{ translate('admin.settings_search_messages') }}</label>
        <input
          id="portal-message-search" ref="search" v-model="query" type="search"
          @input="selected = ''" @keydown.enter.prevent
        >
      </div>
      <div class="catalog-control">
        <label for="portal-catalog-language">{{ translate('admin.settings_translation_language') }}</label>
        <select
          id="portal-catalog-language" ref="languageInput" :value="language"
          @change="emit('update:language', ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="item in languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
            {{ item.native_name }}
          </option>
        </select>
      </div>
      <div v-if="props.messages" class="catalog-control">
        <label for="portal-message-group">{{ translate('admin.settings_message_group') }}</label>
        <select id="portal-message-group" v-model="group" @change="selected = ''">
          <option v-for="(label, choice) in groupLabels" :key="choice" :value="choice">{{ translate(label) }}</option>
        </select>
      </div>
      <div class="catalog-actions">
        <label v-if="props.messages" class="catalog-filter">
          <input v-model="missingOnly" type="checkbox" @change="selected = ''">
          {{ translate('admin.settings_missing_only') }}
        </label>
        <button v-if="activeLanguage && !props.messages" type="button" @click="emit('add-catalog')">
          {{ translate('admin.add_catalog') }}
        </button>
        <button v-if="activeLanguage && props.messages && language !== props.referenceLanguage" type="button" @click="removeCatalog(language)">
          {{ translate('admin.settings_remove_catalog') }}
        </button>
        <button v-for="code in disabledCatalogs" :key="code" type="button" @click="removeCatalog(code)">
          {{ translate('admin.settings_remove_catalog') }} ({{ code }})
        </button>
      </div>
    </div>
    <div class="catalog-browser" :class="{ 'catalog-browser--editing': selected }">
      <div v-if="props.messages" ref="results" class="catalog-list-pane">
        <output v-if="!visible.length">{{ translate('admin.settings_no_messages') }}</output>
        <ul v-else class="catalog-results" :aria-label="translate('admin.settings_select_message')">
          <li v-for="key in visible" :key="key">
            <button type="button" :aria-pressed="selected === key" @click="edit(key)">
              <span :lang="messageLanguage(key)" dir="auto">{{ messageName(key) }}</span>
              <small v-if="duplicateContexts.has(key)">{{ duplicateContexts.get(key) }}</small>
              <small v-if="translationPreview(key)" :lang="language" dir="auto" aria-hidden="true">{{ translationPreview(key) }}</small>
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
        <h3 :lang="messageLanguage(selected)" dir="auto">{{ messageName(selected) }}</h3>
        <small v-if="duplicateContexts.has(selected)">{{ duplicateContexts.get(selected) }}</small>
        <div class="catalog-values" :class="{ 'catalog-values--with-reference': selectedReference }">
          <div v-if="selectedReference" class="catalog-control">
            <p class="catalog-editor-label">
              <strong>{{ translate('admin.settings_reference_text') }}</strong>
              <span :lang="referenceLanguage?.code" :dir="referenceLanguage?.direction">{{ referenceLanguage?.native_name }}</span>
            </p>
            <p class="catalog-reference" :lang="referenceLanguage?.code" dir="auto">{{ selectedReference }}</p>
          </div>
          <div class="catalog-control">
            <div class="catalog-editor-label">
              <label for="portal-message-value">{{ translate('admin.settings_message_value') }}</label>
              <span :lang="activeLanguage?.code" :dir="activeLanguage?.direction">{{ activeLanguage?.native_name }}</span>
            </div>
            <textarea
              id="portal-message-value" ref="input" :value="messages.get(selected) ?? ''"
              :lang="activeLanguage?.code" :dir="activeLanguage?.direction" rows="4" maxlength="4000"
              @input="emit('update:message', selected, ($event.target as HTMLTextAreaElement).value)"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.catalog-workspace, .catalog-control, .catalog-editor, .catalog-browser, .catalog-list-pane, .catalog-values {
  display: grid;
  align-items: start;
  gap: .75rem;
  min-inline-size: 0;
}

.catalog-workspace { gap: 1.25rem; }
.catalog-control { align-content: start; gap: .35rem; }

.catalog-toolbar {
  display: grid;
  gap: .75rem 1rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: end;
}

.catalog-search, .catalog-actions { grid-column: 1 / -1; }

.catalog-workspace :is(button, input:not([type='checkbox']), select, textarea) {
  box-sizing: border-box;
  min-block-size: 44px;
  max-inline-size: 100%;
  min-inline-size: 0;
  padding: .55rem .7rem;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
}

.catalog-workspace :is(input:not([type='checkbox']), select, textarea) { inline-size: 100%; }
.catalog-workspace button { overflow-wrap: anywhere; }
.catalog-workspace button:not(:disabled):hover { background: var(--admin-bg); }
.catalog-workspace button:disabled { color: var(--admin-muted); }

.catalog-actions, .catalog-pagination {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: .5rem 1rem;
}

.catalog-actions button { border-color: transparent; }
.catalog-filter { display: flex; align-items: center; min-block-size: 44px; gap: .5rem; }
.catalog-filter input { width: auto; margin: 0; }

.catalog-list-pane { gap: .5rem; }
.catalog-results {
  list-style: none;
  padding: 0;
  margin: 0;
  border-block-start: 1px solid var(--admin-border);
}

.catalog-results li {
  min-inline-size: 0;
  border-block-end: 1px solid var(--admin-border);
}

.catalog-workspace .catalog-results button {
  display: grid;
  gap: .15rem .85rem;
  align-items: center;
  inline-size: 100%;
  text-align: start;
  border: 0;
  border-inline-start: .2rem solid transparent;
  border-radius: 0;
}

.catalog-workspace .catalog-results button[aria-pressed="true"] {
  border-inline-start-color: var(--admin-action);
  background: var(--admin-bg);
}

.catalog-results :is(span, small) {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}

.catalog-results small, .catalog-editor-label span { color: var(--admin-muted); }
.catalog-editor :is(h3, p) { margin: 0; overflow-wrap: anywhere; }
.catalog-editor textarea { resize: vertical; min-block-size: 9rem; }
.catalog-editor-label { display: flex; flex-wrap: wrap; align-items: baseline; gap: .25rem .5rem; }
.catalog-reference { padding: .7rem; background: var(--admin-bg); border-radius: .4rem; white-space: pre-wrap; }
.catalog-back { justify-self: start; }

@media (max-width: 27rem) {
  .catalog-toolbar { grid-template-columns: minmax(0, 1fr); }
}

@media (min-width: 56rem) {
  .catalog-toolbar { grid-template-columns: minmax(0, 2fr) repeat(2, minmax(0, 1fr)); }
  .catalog-search { grid-column: auto; }
}

@media (min-width: 76rem) {
  .catalog-browser--editing { grid-template-columns: minmax(16rem, 1fr) minmax(0, 2fr); gap: 1.75rem; }
  .catalog-browser--editing .catalog-results { max-block-size: clamp(18rem, 60vh, 40rem); overflow: auto; }
  .catalog-browser:not(.catalog-browser--editing) .catalog-results { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); column-gap: 1.5rem; }
}

@media (min-width: 86rem) {
  .catalog-values--with-reference { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1.25rem; }
}

@media (min-width: 94rem) {
  .catalog-browser:not(.catalog-browser--editing) .catalog-results button:has(small) {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (width < 76rem) {
  .catalog-workspace--editing .catalog-toolbar,
  .catalog-browser--editing .catalog-list-pane { display: none; }
}
</style>
