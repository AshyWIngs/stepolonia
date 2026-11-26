<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { AdminSettingsError, getLanguageDirectory, saveLanguageDirectoryEntry } from '~/utils/admin-site-settings'
import type { LanguageDirectoryEntry, PortalLanguage } from '~/utils/admin-site-settings'

const props = defineProps<{
  visible: boolean
  disabled: boolean
  csrf: string
  catalogCodes: string[] | null
  pendingCodes: string[]
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  changed: [entry: LanguageDirectoryEntry]
  translations: [code: string]
  'editing-state': [state: { dirty: boolean, busy: boolean }]
  'step-up': []
  'session-changed': []
}>()

const entries = ref<LanguageDirectoryEntry[]>([])
const selected = ref<LanguageDirectoryEntry>()
const draft = ref<PortalLanguage>()
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const conflict = ref(false)
const failure = ref('')
const saved = ref<PortalLanguage>()
const addButton = ref<HTMLButtonElement>()
const codeInput = ref<HTMLInputElement>()
const nameInput = ref<HTMLInputElement>()
const saveStatus = ref<HTMLParagraphElement>()
const dirty = computed(() => draft.value !== undefined && JSON.stringify(draft.value) !== JSON.stringify(selected.value?.language))

const disablePending = computed(() => selected.value?.enabled && props.pendingCodes.includes(selected.value.language.code))

watch([dirty, saving], ([dirty, busy]) => emit('editing-state', { dirty, busy }))
watch(() => props.visible, (visible) => {
  if (visible && !loaded.value) void load()
}, { immediate: true })

const messages: Record<string, string> = {
  invalid_language_entry: 'admin.language_directory_invalid',
  validation_error: 'admin.language_directory_invalid',
  language_limit: 'admin.language_directory_limit',
  language_used_in_settings: 'admin.language_directory_used_settings',
  language_used_in_content: 'admin.language_directory_used_content',
  site_draft_pending: 'admin.settings_scoped_pending',
  permission_denied: 'admin.access_denied',
  rate_limited: 'admin.limited',
}

function showError(error: unknown) {
  if (!(error instanceof AdminSettingsError)) {
    failure.value = 'admin.unavailable'
    return
  }
  if (error.code === 'step_up_required') {
    emit('step-up')
    failure.value = 'admin.step_up_required'
  } else if (error.status === 401 || error.code === 'csrf_failed') {
    emit('session-changed')
    failure.value = 'admin.changed'
  } else if (error.code === 'site_version_conflict') {
    conflict.value = selected.value !== undefined
    failure.value = selected.value ? 'admin.settings_conflict' : 'admin.language_directory_duplicate'
  } else {
    failure.value = messages[error.code ?? ''] ?? 'admin.unavailable'
  }
}

async function load() {
  if (loading.value) return
  loading.value = true
  try {
    entries.value = await getLanguageDirectory()
    loaded.value = true
    const current = entries.value.find(entry => entry.language.code === selected.value?.language.code)
    if (current) rebase(current)
    else if (!draft.value && entries.value[0]) select(entries.value[0])
  } catch (error: unknown) {
    showError(error)
  } finally {
    loading.value = false
  }
}

function select(entry: LanguageDirectoryEntry) {
  selected.value = entry
  draft.value = { ...entry.language }
  conflict.value = false
  failure.value = ''
  saved.value = undefined
}

function rebase(entry: LanguageDirectoryEntry) {
  const before = selected.value?.language
  const changes = draft.value
  const keepEdits = conflict.value && before && changes
  select(entry)
  if (keepEdits) {
    draft.value = {
      ...entry.language,
      name: changes.name === before.name ? entry.language.name : changes.name,
      native_name: changes.native_name === before.native_name ? entry.language.native_name : changes.native_name,
      direction: changes.direction === before.direction ? entry.language.direction : changes.direction,
    }
  }
}

async function add() {
  selected.value = undefined
  draft.value = { code: '', name: '', native_name: '', direction: 'ltr' }
  conflict.value = false
  failure.value = ''
  saved.value = undefined
  await nextTick()
  codeInput.value?.focus()
}

async function cancel() {
  const wasEditing = selected.value !== undefined
  const entry = selected.value ?? entries.value[0]
  if (entry) select(entry)
  else {
    draft.value = undefined
    failure.value = ''
    conflict.value = false
  }
  await nextTick()
  if (wasEditing) nameInput.value?.focus()
  else addButton.value?.focus()
}

async function mutate(kind: 'metadata' | 'availability') {
  if (!draft.value || props.disabled || saving.value || conflict.value) return
  const entry = selected.value
  if (kind === 'availability' && (!entry || dirty.value || disablePending.value)) return
  const command = kind === 'availability' && entry
    ? { code: entry.language.code, enabled: !entry.enabled, expected_version: entry.version }
    : { language: draft.value, expected_version: entry?.version }
  saving.value = true
  saved.value = undefined
  failure.value = ''
  try {
    const result = await saveLanguageDirectoryEntry(props.csrf, command)
    entries.value = [...entries.value.filter(item => item.language.code !== result.language.code), result]
      .sort((a, b) => a.language.code.localeCompare(b.language.code))
    select(result)
    saved.value = result.language
    emit('changed', result)
    if (!entry) {
      await nextTick()
      saveStatus.value?.focus()
    }
  } catch (error: unknown) {
    showError(error)
  } finally {
    saving.value = false
  }
}

function statusKey(entry: LanguageDirectoryEntry) {
  if (!entry.enabled) return 'admin.language_directory_disabled'
  return hasCatalog(entry) ? 'admin.language_directory_published' : 'admin.language_directory_no_catalog'
}

function hasCatalog(entry: LanguageDirectoryEntry) {
  return props.catalogCodes?.includes(entry.language.code) ?? entry.catalog_published
}
</script>

<template>
  <section class="admin-domain language-directory" aria-labelledby="language-directory-heading" :aria-busy="loading || saving">
    <AdminDomainHeader :title="translate('admin.language_directory')" heading-id="language-directory-heading" />
    <p v-if="loading && !loaded" role="status">{{ translate('admin.working') }}</p>
    <p v-if="failure" class="admin-error" role="alert">{{ translate(failure) }}</p>
    <button v-if="!loading && (!loaded || conflict && selected)" type="button" :disabled="saving || disabled" @click="load">
      {{ translate(loaded ? 'admin.settings_scoped_reload' : 'admin.retry') }}
    </button>
    <p v-if="saved" ref="saveStatus" role="status" tabindex="-1">
      {{ translate('admin.settings_saved') }} <bdi :lang="saved.code" :dir="saved.direction">{{ saved.native_name }}</bdi>
    </p>
    <div v-if="loaded" class="language-directory-grid">
      <div class="language-directory-list">
        <button ref="addButton" type="button" :disabled="disabled || dirty || saving" @click="add">{{ translate('admin.language_directory_add') }}</button>
        <ul>
          <li v-for="item in entries" :key="item.language.code">
            <button
              type="button" :aria-pressed="selected?.language.code === item.language.code"
              :disabled="dirty || saving || loading" @click="select(item)"
            >
              <bdi :lang="item.language.code" :dir="item.language.direction">{{ item.language.native_name }}</bdi>
              <code>{{ item.language.code }}</code>
              <span>{{ translate(statusKey(item)) }}</span>
            </button>
          </li>
        </ul>
      </div>
      <form v-if="draft" class="language-directory-form" @submit.prevent="mutate('metadata')">
        <fieldset :disabled="disabled || loading || saving">
          <div>
            <label for="language-directory-code">{{ translate('admin.language_directory_code') }}</label>
            <input id="language-directory-code" ref="codeInput" v-model="draft.code" :readonly="!!selected" minlength="2" maxlength="63" required dir="ltr" autocomplete="off">
          </div>
          <div>
            <label for="language-directory-name">{{ translate('admin.language_directory_name') }}</label>
            <input id="language-directory-name" ref="nameInput" v-model="draft.name" maxlength="200" required dir="auto" autocomplete="off">
          </div>
          <div>
            <label for="language-directory-native">{{ translate('admin.language_directory_native') }}</label>
            <input id="language-directory-native" v-model="draft.native_name" maxlength="200" required dir="auto" autocomplete="off">
          </div>
          <div>
            <label for="language-directory-direction">{{ translate('admin.language_directory_direction') }}</label>
            <select id="language-directory-direction" v-model="draft.direction">
              <option value="ltr">{{ translate('admin.language_directory_ltr') }}</option>
              <option value="rtl">{{ translate('admin.language_directory_rtl') }}</option>
            </select>
          </div>
          <div class="language-directory-actions">
            <button v-if="dirty" type="submit" class="settings-primary" :disabled="conflict">{{ translate('admin.settings_save_changes') }}</button>
            <button v-if="dirty" type="button" @click="cancel">{{ translate('admin.cancel') }}</button>
            <button v-if="selected && !dirty" type="button" :disabled="conflict || disablePending" @click="mutate('availability')">
              {{ translate(selected.enabled ? 'admin.language_directory_disable' : 'admin.language_directory_enable') }}
            </button>
            <button v-if="selected?.enabled && !dirty && !hasCatalog(selected)" type="button" @click="emit('translations', selected.language.code)">
              {{ translate('admin.language_directory_edit_catalog') }}
            </button>
          </div>
          <p v-if="disablePending">{{ translate('admin.language_directory_enabled_unused') }}</p>
        </fieldset>
      </form>
    </div>
  </section>
</template>

<style scoped>
.language-directory-grid, .language-directory-form fieldset {
  display: grid;
  gap: .75rem;
  min-inline-size: 0;
}
.language-directory-list ul {
  display: grid;
  gap: .35rem;
  padding: 0;
  list-style: none;
}
.language-directory-list li button {
  display: grid;
  width: 100%;
  text-align: start;
}
.language-directory-list bdi, .language-directory-list code, .language-directory-list span {
  overflow-wrap: anywhere;
}
.language-directory button, .language-directory input, .language-directory select {
  min-block-size: 44px;
}
.language-directory button[aria-pressed="true"] {
  outline: 2px solid var(--admin-accent, currentColor);
  outline-offset: -2px;
}
.language-directory-form fieldset {
  margin: 0;
  padding: 0;
  border: 0;
}
.language-directory-actions {
  display: flex;
  flex-wrap: wrap;
  gap: .5rem;
}
@media (min-width: 58rem) {
  .language-directory-grid { grid-template-columns: minmax(13rem, 1fr) minmax(15rem, 2fr); }
}
</style>
