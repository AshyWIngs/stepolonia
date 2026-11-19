<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { AdminSettingsError, getPortalSettings, savePortalSettings } from '~/utils/admin-site-settings'
import type { EditablePortal, PortalSettingsPage } from '~/utils/admin-site-settings'
import { invalidSlugRuleIndex, normalizeSlugCharacter, type SlugRule } from '~/utils/slug-suggestion'

const props = defineProps<{
  page: PortalSettingsPage
  csrf: string
  uiLanguage: string
  uiMessages: Record<string, string>
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  published: []
  'show-page': [page: PortalSettingsPage]
  'editing-state': [state: { dirty: boolean, busy: boolean }]
  'step-up': []
  'session-changed': []
}>()

const editableSettings = ref<EditablePortal | null>(null)
const selectedLanguage = ref('')
const translations = ref<{ showMissing: () => Promise<void> }>()
const slugRules = ref<{ showInvalid: (index: number) => Promise<void> }>()
const flagEditor = ref<{ focusInvalid: () => Promise<void> }>()
const flagUpdates = ref<Record<string, string>>({})
const publicationStatus = ref<HTMLOutputElement | null>(null)
const busy = ref(false)
const dirty = ref(false)
watch([() => dirty.value, busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const conflict = ref(false)
const failure = ref('')
const notice = ref('')
let active = true

const language = computed(() => editableSettings.value?.languages.find(item => item.code === selectedLanguage.value))
const catalogsByLanguage = computed(() => new Map(Object.entries(editableSettings.value?.catalogs ?? {})))
const messages = computed(() => catalogsByLanguage.value.get(selectedLanguage.value))
const reference = computed(() => catalogsByLanguage.value.get(editableSettings.value?.settings.fallback_language ?? ''))
const catalogLanguages = computed(() => {
  const editable = editableSettings.value
  return editable?.languages.filter(item => catalogsByLanguage.value.has(item.code)) ?? []
})
const disabledCatalogCodes = computed(() => {
  const editable = editableSettings.value
  if (!editable) return []
  const enabled = new Set(editable.languages.map(item => item.code))
  return Object.keys(editable.catalogs).filter(code => !enabled.has(code) && code !== editable.settings.fallback_language)
})
const accentPattern = /^#[\da-f]{6}$/iu
const pickerColor = computed(() => {
  const accent = editableSettings.value?.settings.accent
  return accent && accentPattern.test(accent) ? accent : undefined
})

function errorMessage(key: string) {
  failure.value = key
}

function handleFailure(error: unknown) {
  if (!(error instanceof AdminSettingsError)) {
    errorMessage('admin.unavailable')
    return
  }
  if (error.code === 'step_up_required') {
    emit('step-up')
    return
  }
  if (error.code === 'invalid_language_flag') {
    errorMessage('admin.language_flag_invalid')
    void showPage('languages').then(() => flagEditor.value?.focusInvalid())
    return
  }
  if (error.status === 401 || error.code === 'csrf_failed') {
    emit('session-changed')
    errorMessage('admin.changed')
    return
  }
  switch (error.status) {
    case 400:
    case 413:
      errorMessage('admin.settings_invalid')
      break
    case 403:
      editableSettings.value = null
      dirty.value = false
      errorMessage('admin.access_denied')
      break
    case 409:
      conflict.value = true
      errorMessage('admin.settings_conflict')
      break
    default:
      errorMessage('admin.unavailable')
  }
}

async function load() {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  notice.value = ''
  try {
    const editable = await getPortalSettings()
    if (!active) return
    editableSettings.value = editable
    flagUpdates.value = {}
    selectedLanguage.value = editable.languages.some(item => item.code === props.uiLanguage)
      ? props.uiLanguage : editable.settings.fallback_language
    dirty.value = false
    conflict.value = false
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    busy.value = false
  }
}

function addCatalog() {
  const editable = editableSettings.value
  if (!editable || !language.value || messages.value) return
  editable.catalogs = { ...editable.catalogs, [language.value.code]: {} }
  dirty.value = true
}

function removeCatalog(code: string) {
  const editable = editableSettings.value
  if (!editable || code === editable.settings.fallback_language) return
  editable.catalogs = Object.fromEntries(Object.entries(editable.catalogs).filter(([locale]) => locale !== code))
  dirty.value = true
}

async function showPage(target: PortalSettingsPage) {
  if (props.page === target) return
  emit('show-page', target)
  await nextTick()
}

function changeMessage(key: string, value: string) {
  const editable = editableSettings.value
  const entries = messages.value
  if (!editable || !entries || !editable.message_keys.includes(key)) return
  const updated = new Map(Object.entries(entries))
  updated.set(key, value)
  const catalogs = new Map(catalogsByLanguage.value)
  catalogs.set(selectedLanguage.value, Object.fromEntries(updated))
  editable.catalogs = Object.fromEntries(catalogs)
  dirty.value = true
}

function updateSlugRules(rules: SlugRule[]) {
  if (!editableSettings.value) return
  editableSettings.value.slug_rules = rules
  dirty.value = true
  if (failure.value === 'admin.slug_rules_invalid') failure.value = ''
}

function updateLanguageFlags(flags: Record<string, string>) {
  flagUpdates.value = flags
  dirty.value = true
  if (failure.value === 'admin.language_flag_invalid') failure.value = ''
}

function invalidSettingsSection(editable: EditablePortal): PortalSettingsPage | undefined {
  const settings = editable.settings
  if (!settings.title.trim()) return 'identity'
  if (!accentPattern.test(settings.accent)) return 'appearance'
  if (!editable.languages.some(language => language.code === settings.default_language) ||
      !catalogLanguages.value.some(language => language.code === settings.fallback_language)) return 'languages'
  return undefined
}

async function validateSettings(editable: EditablePortal) {
  const invalidSection = invalidSettingsSection(editable)
  if (invalidSection) {
    errorMessage('admin.settings_invalid')
    await showPage(invalidSection)
    return false
  }
  const invalidRule = invalidSlugRuleIndex(editable.slug_rules)
  if (invalidRule >= 0) {
    notice.value = ''
    errorMessage('admin.slug_rules_invalid')
    await showPage('languages')
    await slugRules.value?.showInvalid(invalidRule)
    return false
  }
  const incomplete = incompleteCatalog(editable)
  if (incomplete) {
    selectedLanguage.value = incomplete
    notice.value = ''
    errorMessage('admin.settings_complete_catalog')
    await showPage('translations')
    await translations.value?.showMissing()
    return false
  }
  return true
}

async function save() {
  const editable = editableSettings.value
  if (!editable || busy.value || conflict.value || (!dirty.value && !editable.has_draft)) return
  if (!await validateSettings(editable)) return
  const rules = editable.slug_rules.map(rule => ({ ...rule, character: normalizeSlugCharacter(rule.character) }))
  busy.value = true
  failure.value = ''
  notice.value = ''
  try {
    const result = await savePortalSettings(props.csrf, {
      expected_version: editable.version,
      settings: editable.settings,
      catalogs: editable.catalogs,
      slug_rules: rules,
      ...(Object.keys(flagUpdates.value).length ? { language_flags: flagUpdates.value } : {}),
    })
    if (!active) return
    editable.version = result.version
    editable.published_id = result.publicationID
    editable.has_draft = false
    editable.slug_rules = rules
    editable.language_flags = { ...editable.language_flags, ...result.languageFlags }
    flagUpdates.value = {}
    dirty.value = false
    notice.value = 'admin.settings_saved'
    emit('published')
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    busy.value = false
  }
  if (active && notice.value === 'admin.settings_saved') {
    await nextTick()
    publicationStatus.value?.focus()
  }
}

function incompleteCatalog(editable: EditablePortal): string | undefined {
  return Object.entries(editable.catalogs).find(([, entries]) => {
    const nonBlankKeys = new Set(Object.entries(entries).filter(([, text]) => text.trim()).map(([key]) => key))
    return editable.message_keys.some(key => !nonBlankKeys.has(key))
  })?.[0]
}

onMounted(async () => {
  await load()
})
onBeforeUnmount(() => { active = false })
</script>

<template>
  <div class="portal-settings" :aria-busy="busy">
    <output v-if="notice" ref="publicationStatus" tabindex="-1">{{ translate(notice) }}</output>
    <output v-if="busy">{{ translate('admin.working') }}</output>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <button v-if="conflict" type="button" :disabled="busy" @click="load">{{ translate('admin.reload_settings') }}</button>
    <button v-else-if="!editableSettings && failure" type="button" :disabled="busy" @click="load">{{ translate('admin.retry') }}</button>

    <form v-if="editableSettings" @submit.prevent="save">
      <fieldset v-if="page === 'identity'" class="settings-fields settings-field-grid" :disabled="busy" @input="dirty = true">
        <div class="settings-field">
          <label for="portal-title">{{ translate('admin.portal_name') }}</label>
          <input id="portal-title" v-model="editableSettings.settings.title" maxlength="200" required>
        </div>
        <div class="settings-field">
          <label for="portal-tagline">{{ translate('admin.tagline') }}</label>
          <input id="portal-tagline" v-model="editableSettings.settings.tagline" maxlength="300">
        </div>
        <div class="settings-field">
          <label for="portal-description">{{ translate('admin.description') }}</label>
          <textarea id="portal-description" v-model="editableSettings.settings.description" maxlength="2000" rows="4" />
        </div>
        <div class="settings-field">
          <label for="portal-notice">{{ translate('admin.notice') }}</label>
          <textarea id="portal-notice" v-model="editableSettings.settings.notice" maxlength="2000" rows="4" />
        </div>
      </fieldset>

      <fieldset v-else-if="page === 'appearance'" class="settings-fields" :disabled="busy" @input="dirty = true">
        <label for="portal-accent">{{ translate('admin.accent') }}</label>
        <div class="settings-accent">
          <input id="portal-accent" v-model="editableSettings.settings.accent" pattern="#[0-9a-fA-F]{6}" required>
          <input
            type="color" :aria-label="translate('admin.settings_choose_accent')" :value="pickerColor"
            @input="editableSettings.settings.accent = ($event.target as HTMLInputElement).value"
          >
        </div>
      </fieldset>

      <fieldset v-else-if="page === 'languages'" class="settings-fields settings-field-grid" :disabled="busy" @change="dirty = true">
        <div class="settings-field">
          <label for="portal-default-language">{{ translate('admin.default_language') }}</label>
          <select id="portal-default-language" v-model="editableSettings.settings.default_language" required>
            <option v-for="item in editableSettings.languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
              {{ item.native_name }}
            </option>
          </select>
        </div>
        <div class="settings-field">
          <label for="portal-fallback-language">{{ translate('admin.fallback_language') }}</label>
          <select id="portal-fallback-language" v-model="editableSettings.settings.fallback_language" required>
            <option v-for="item in catalogLanguages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
              {{ item.native_name }}
            </option>
          </select>
        </div>
      </fieldset>

      <fieldset v-show="page === 'languages'" class="settings-fields" :disabled="busy">
        <PortalLanguageFlags
          ref="flagEditor" :model-value="flagUpdates" :languages="editableSettings.languages"
          :flags="editableSettings.language_flags ?? {}" :default-language="uiLanguage" :translate="translate"
          :invalid="failure === 'admin.language_flag_invalid'" @update:model-value="updateLanguageFlags"
        />
      </fieldset>

      <fieldset v-show="page === 'languages'" class="settings-fields" :disabled="busy">
        <PortalSlugRules
          ref="slugRules" :rules="editableSettings.slug_rules" :languages="editableSettings.languages"
          :default-language="editableSettings.settings.default_language" :translate="translate" @update:rules="updateSlugRules"
        />
      </fieldset>

      <fieldset v-show="page === 'translations'" class="settings-translations" :disabled="busy">
        <PortalTranslationSettings
          ref="translations" v-model:language="selectedLanguage" :languages="editableSettings.languages" :keys="editableSettings.message_keys"
          :messages="messages" :reference="reference" :reference-language="editableSettings.settings.fallback_language"
          :ui-language="uiLanguage" :ui-messages="uiMessages" :disabled-catalogs="disabledCatalogCodes" :translate="translate"
          @add-catalog="addCatalog" @remove-catalog="removeCatalog" @update:message="changeMessage"
        />
      </fieldset>

      <p v-if="editableSettings.has_draft">{{ translate('admin.settings_pending') }}</p>
      <div v-if="!conflict && (dirty || editableSettings.has_draft)" class="settings-actions">
        <button type="submit" class="settings-primary" :disabled="busy">{{ translate('admin.settings_save_changes') }}</button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.portal-settings {
  display: grid;
  align-items: start;
  gap: .75rem;
  min-inline-size: 0;
}

.settings-actions {
  display: flex;
  flex-wrap: wrap;
  gap: .5rem;
}

.portal-settings p {
  margin: 0;
  overflow-wrap: anywhere;
}

.portal-settings form,
.settings-fields {
  display: grid;
  gap: 0.65rem;
  min-inline-size: 0;
}

.settings-field { display: grid; align-self: start; gap: .35rem; min-inline-size: 0; }

@media (min-width: 62rem) {
  .settings-field-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem 1.5rem; }
}

.settings-accent {
  display: flex;
  gap: .5rem;
}

.settings-accent input[type='color'] {
  flex: 0 0 3.5rem;
  width: 3.5rem;
  padding: .35rem;
}

.settings-fields {
  margin: 0;
  padding: 0.75rem;
  border: 1px solid var(--admin-border);
  border-radius: 0.4rem;
}

.portal-settings :deep(:is(input:not([type='checkbox']), select, textarea)),
.portal-settings button {
  box-sizing: border-box;
  max-inline-size: 100%;
  min-block-size: 44px;
  min-inline-size: 0;
  padding: 0.55rem 0.75rem;
  border: 1px solid var(--admin-border);
  border-radius: 0.4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
}

.portal-settings :focus-visible {
  outline: 3px solid var(--admin-focus);
  outline-offset: 3px;
}

.portal-settings button:not(:disabled) {
  cursor: pointer;
}

.portal-settings textarea {
  resize: vertical;
}

.settings-translations {
  min-inline-size: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.settings-actions button { overflow-wrap: break-word; }

.portal-settings .settings-primary {
  background: var(--admin-action);
  color: var(--admin-action-text);
}
</style>
