<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getPortalSettings } from '~/utils/admin-site-settings'
import type { LanguageDirectoryEntry, EditablePortal, PortalSettingsPage, ScopedSettingsSection } from '~/utils/admin-site-settings'
import { invalidSlugRuleIndex, type SlugRule } from '~/utils/slug-suggestion'
import { PortalCatalogDrafts, incompleteCatalog } from '~/features/portal-settings/PortalCatalogDrafts'
import { PortalRevisionPublication } from '~/features/portal-settings/PortalRevisionPublication'
import { PortalSiteDrafts, isPortalAccent } from '~/features/portal-settings/PortalSiteDrafts'
import { PortalSlugRuleDraft } from '~/features/portal-settings/PortalSlugRuleDraft'
import { ScopedPortalPublication } from '~/features/portal-settings/ScopedPortalPublication'

const props = defineProps<{
  page: PortalSettingsPage
  permissionTexts: boolean
  csrf: string
  uiLanguage: string
  uiMessages: Record<string, string>
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  published: []
  'show-page': [page: PortalSettingsPage]
  'update:permission-texts': [enabled: boolean]
  'editing-state': [state: { dirty: boolean, busy: boolean }]
  'step-up': []
  'session-changed': []
}>()

const editableSettings = ref<EditablePortal | null>(null)
const savedValueDifferences = ref(new Map<string, string[]>())
const translate = (key: string) => props.translate(key)
const removedValueLabel = () => translate('admin.settings_removed_value')
const recordSavedValues = (scope: string, values: string[]) => savedValueDifferences.value.set(scope, values)
const catalogDrafts = new PortalCatalogDrafts(editableSettings, localeLabel, translate, recordSavedValues)
const siteDrafts = new PortalSiteDrafts(editableSettings, removedValueLabel, recordSavedValues)
const slugRuleDraft = new PortalSlugRuleDraft(editableSettings, localeLabel, removedValueLabel, recordSavedValues)
const {
  selectedLanguage, catalogTarget, registryRepairRequired, registryKeysReloaded, editedCatalogCodes, dirty,
  messages, reference, catalogLanguages, disabledCatalogCodes,
} = catalogDrafts
const { detailsDraft, detailsDirty, localeDefaultsDraft, localeDefaultsDirty, flagUpdates, flagLanguage, accentDraft, pickerColor } = siteDrafts
const { rules: slugRulesDraft, dirty: slugRulesDirty } = slugRuleDraft
const translations = ref<{ showMissing: () => Promise<void>, focusActive: () => Promise<void> }>()
const slugRules = ref<{ showInvalid: (index: number) => Promise<void>, focusActive: () => Promise<void> }>()
const flagEditor = ref<{ focusInvalid: () => Promise<void> }>()
const settingsPanel = ref<HTMLDivElement>()
const accentInput = ref<HTMLInputElement>()
const titleInput = ref<HTMLInputElement>()
const defaultLanguageInput = ref<HTMLSelectElement>()
const publicationStatus = ref<HTMLOutputElement | null>(null)
const languageEditing = ref({ dirty: false, busy: false })
let active = true

const publication = new ScopedPortalPublication({
  settings: editableSettings, catalogs: catalogDrafts, site: siteDrafts, slugRules: slugRuleDraft, savedValueDifferences,
  section: pageScope(props.page),
  csrf: () => props.csrf,
  isActive: () => active,
  otherSaveInProgress: () => busy.value || languageEditing.value.busy,
  translate,
  started: () => { notice.value = '' },
  published: () => emit('published'),
  sessionChanged: () => emit('session-changed'),
})
const {
  busy: scopedBusy, notice: scopedNotice, failure: scopedFailure, pendingDraftConflict,
  hasReportedConflict: scopedConflict,
} = publication
const revision: PortalRevisionPublication = new PortalRevisionPublication({
  settings: editableSettings, catalogs: catalogDrafts, scoped: publication,
  csrf: () => props.csrf,
  uiLanguage: () => props.uiLanguage,
  isActive: () => active,
  syncDrafts: loadScopeEditors,
  validate: validateSettings,
  invalidFlag: () => { void showPage('languages').then(() => flagEditor.value?.focusInvalid()) },
  stepUp: () => emit('step-up'),
  sessionChanged: () => emit('session-changed'),
  published: () => emit('published'),
})
const { busy, notice, conflict, failure } = revision

const hasEdits = computed(() => languageEditing.value.dirty || dirty.value || siteDrafts.dirty.value || slugRulesDirty.value)
watch([hasEdits, () => busy.value || scopedBusy.value || languageEditing.value.busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})

function errorMessage(key: string) {
  failure.value = key
}

function pageScope(page: PortalSettingsPage): ScopedSettingsSection {
  if (page === 'identity') return 'details'
  if (page === 'translations') return 'catalogs'
  return page === 'appearance' ? 'appearance' : 'language-flags'
}

function loadScopeEditors(editable: EditablePortal) {
  siteDrafts.sync(editable)
  slugRuleDraft.sync(editable)
}

async function refreshLanguageSettings(entry: LanguageDirectoryEntry) {
  scopedBusy.value = true
  try {
    const fresh = await getPortalSettings()
    if (!active) return
    if (editableSettings.value && dirty.value) fresh.version = editableSettings.value.version
    catalogDrafts.preserveCatalogVersions(fresh)
    loadScopeEditors(fresh)
    editableSettings.value = fresh
    if (!entry.enabled) {
      if (flagLanguage.value === entry.language.code) flagLanguage.value = fresh.settings.fallback_language
      if (selectedLanguage.value === entry.language.code) selectedLanguage.value = fresh.settings.fallback_language
    }
    emit('published')
  } catch (error: unknown) {
    if (active) revision.handleFailure(error)
  } finally {
    scopedBusy.value = false
  }
}

async function showLanguageTranslations(code: string) {
  selectedLanguage.value = code
  await showPage('translations')
  await translations.value?.focusActive()
}

async function showPage(target: PortalSettingsPage) {
  if (props.page === target) return
  emit('show-page', target)
  await nextTick()
}

function updateSlugRules(rules: SlugRule[]) {
  slugRulesDraft.value = rules
  if (scopedFailure.value === 'admin.slug_rules_invalid') scopedFailure.value = ''
  if (failure.value === 'admin.slug_rules_invalid') failure.value = ''
}

function updateLanguageFlags(flags: Record<string, string>) {
  flagUpdates.value = flags
  if (scopedFailure.value === 'admin.language_flag_invalid') scopedFailure.value = ''
}

watch(() => props.page, () => { notice.value = '' })

watch([() => props.page, flagLanguage, selectedLanguage], () => publication.selectScope(pageScope(props.page)))

async function saveScoped(scope: ScopedSettingsSection) {
  const saved = await publication.save(scope, (editable, code) => validateScopedDraft(editable, scope, code))
  if (saved) await focusScopedResult(scope)
}

async function focusScopedResult(scope: ScopedSettingsSection) {
  if (!active || pendingDraftConflict.value || scopedFailure.value === 'admin.changed') return
  if (publication.hasConflict(scope)) {
    await nextTick()
    settingsPanel.value?.querySelector<HTMLButtonElement>(`[data-reload-scope="${scope}"]`)?.focus()
  } else if (scopedFailure.value) {
    await focusScopeControl(scope)
  } else if (scopedNotice.value) {
    await nextTick()
    settingsPanel.value?.querySelector<HTMLOutputElement>(`[data-saved-scope="${scope}"]`)?.focus()
  }
}

async function validateScopedDraft(editable: EditablePortal, scope: ScopedSettingsSection, code: string) {
  if (scope === 'slug-rules') return validateSlugRulesDraft()
  if (scope === 'catalogs') return canSaveCatalog(editable, code)
  return true
}

async function canSaveCatalog(editable: EditablePortal, code: string) {
  const completeness = catalogDrafts.catalogCompleteness(editable, code)
  if (completeness !== 'incomplete') return completeness === 'complete'
  scopedNotice.value = ''
  scopedFailure.value = 'admin.settings_complete_catalog'
  await translations.value?.showMissing()
  return false
}

async function validateSlugRulesDraft() {
  const index = invalidSlugRuleIndex(slugRulesDraft.value)
  if (index < 0) return true
  scopedNotice.value = ''
  scopedFailure.value = 'admin.slug_rules_invalid'
  await slugRules.value?.showInvalid(index)
  return false
}

async function refreshScoped(scope: ScopedSettingsSection) {
  if (await publication.refresh(scope)) await refocusScopedInput(scope)
}

function localeLabel(code: string) {
  return editableSettings.value?.languages.find(language => language.code === code)?.native_name ?? code
}

async function refocusScopedInput(scope: ScopedSettingsSection) {
  if (!active || scopedConflict.value || scopedFailure.value) return
  await focusScopeControl(scope)
}

async function focusScopeControl(scope: ScopedSettingsSection) {
  await nextTick()
  switch (scope) {
    case 'details':
      titleInput.value?.focus()
      break
    case 'appearance':
      accentInput.value?.focus()
      break
    case 'locale-defaults':
      defaultLanguageInput.value?.focus()
      break
    case 'slug-rules':
      await slugRules.value?.focusActive()
      break
    case 'catalogs':
      await translations.value?.focusActive()
      break
    default:
      await flagEditor.value?.focusInvalid()
  }
}

async function reviewPendingDraft() {
  if (pendingDraftConflict.value && !dirty.value) await revision.load()
  if (pendingDraftConflict.value) {
    conflict.value = true
    errorMessage('admin.settings_conflict')
  }
  await showPage('identity')
}

function invalidSettingsSection(editable: EditablePortal): PortalSettingsPage | undefined {
  const settings = editable.settings
  if (!settings.title.trim()) return 'identity'
  if (!isPortalAccent(settings.accent)) return 'appearance'
  if (!editable.languages.some(language => language.code === settings.default_language) ||
      !catalogLanguages.value.some(language => language.code === settings.fallback_language)) return 'languages'
  return undefined
}

async function validateSettings(editable: EditablePortal, catalogs: EditablePortal['catalogs']) {
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
  const incomplete = incompleteCatalog(catalogs, editable.message_keys)
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
  if (await revision.publish()) {
    await nextTick()
    publicationStatus.value?.focus()
  }
}

onMounted(() => revision.load())
onBeforeUnmount(() => {
  active = false
  publication.dispose()
})
</script>

<template>
  <div ref="settingsPanel" class="portal-settings" :class="{ 'portal-settings--languages': page === 'languages' }" :aria-busy="busy || scopedBusy">
    <output v-if="notice" ref="publicationStatus" tabindex="-1">{{ translate(notice) }}</output>
    <output v-if="busy">{{ translate('admin.working') }}</output>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <button v-if="conflict" type="button" :disabled="busy || scopedBusy" @click="revision.load()">{{ translate('admin.reload_settings') }}</button>
    <button v-else-if="!editableSettings && failure" type="button" :disabled="busy || scopedBusy" @click="revision.load()">{{ translate('admin.retry') }}</button>

    <template v-if="page !== 'identity' && (editableSettings?.has_draft || pendingDraftConflict)">
      <p><output>{{ translate('admin.settings_scoped_pending') }}</output></p>
      <div><button type="button" :disabled="busy || scopedBusy" @click="reviewPendingDraft">{{ translate('admin.settings_identity') }}</button></div>
    </template>

    <form v-if="editableSettings && page === 'appearance'" class="admin-domain" @submit.prevent="saveScoped('appearance')">
      <AdminDomainHeader
        :save-label="translate('admin.settings_save_appearance')"
        :disabled="publication.saveBlocked('appearance') || accentDraft === editableSettings.settings.accent"
      />
      <PortalScopeFeedback v-bind="publication.feedback('appearance')" @reload="refreshScoped('appearance')" />
      <fieldset class="settings-fields" :disabled="busy || scopedBusy || editableSettings.has_draft || pendingDraftConflict">
        <label for="portal-accent">{{ translate('admin.accent') }}</label>
        <div class="settings-accent">
          <input id="portal-accent" ref="accentInput" v-model="accentDraft" aria-describedby="portal-accent-help" pattern="#[0-9a-fA-F]{6}" required>
          <input
            type="color" :aria-label="translate('admin.settings_choose_accent')" :value="pickerColor"
            @input="accentDraft = ($event.target as HTMLInputElement).value"
          >
        </div>
        <p id="portal-accent-help">{{ translate('admin.settings_accent_help') }} <code>#536f89</code></p>
      </fieldset>
    </form>

    <PortalLanguageDirectory
      v-if="editableSettings" v-show="page === 'languages'"
      :visible="page === 'languages'" :disabled="busy || scopedBusy || editableSettings.has_draft || pendingDraftConflict"
      :csrf="csrf" :translate="translate" :catalog-codes="editableSettings.has_draft ? null : Object.keys(editableSettings.catalogs)"
      :pending-codes="[...editedCatalogCodes, ...Object.keys(flagUpdates)]"
      @changed="refreshLanguageSettings" @translations="showLanguageTranslations"
      @editing-state="languageEditing = $event" @step-up="emit('step-up')" @session-changed="emit('session-changed')"
    />

    <form v-if="editableSettings" v-show="page === 'languages'" class="admin-domain domain--flags" aria-labelledby="portal-flags-heading" @submit.prevent="saveScoped('language-flags')">
      <AdminDomainHeader
        :title="translate('admin.language_flags')" heading-id="portal-flags-heading" :save-label="translate('admin.language_flag_save')"
        :disabled="publication.saveBlocked('language-flags') || !Object.hasOwn(flagUpdates, flagLanguage)"
      />
      <PortalScopeFeedback v-bind="publication.feedback('language-flags')" @reload="refreshScoped('language-flags')" />
      <fieldset class="settings-fields" :disabled="busy || scopedBusy || editableSettings.has_draft || pendingDraftConflict">
        <PortalLanguageFlags
          ref="flagEditor" v-model:language="flagLanguage" :model-value="flagUpdates" :languages="editableSettings.languages"
          :flags="editableSettings.language_flags ?? {}" :default-language="uiLanguage" :translate="translate"
          :invalid="scopedFailure === 'admin.language_flag_invalid'" @update:model-value="updateLanguageFlags"
        />
      </fieldset>
    </form>

    <form
      v-if="editableSettings && page === 'identity' && detailsDraft"
      class="admin-domain"
      @submit.prevent="editableSettings.has_draft ? save() : saveScoped('details')">
      <AdminDomainHeader
        v-if="!editableSettings.has_draft" :save-label="translate('admin.settings_save_changes')"
        :disabled="!detailsDirty || publication.saveBlocked('details') || conflict"
      />
      <div v-else class="settings-actions">
        <button type="submit" class="settings-primary" :disabled="busy || scopedBusy || conflict || pendingDraftConflict">
          {{ translate('admin.settings_apply_pending') }}
        </button>
      </div>
      <PortalScopeFeedback v-bind="publication.feedback('details')" @reload="refreshScoped('details')" />
      <fieldset class="settings-fields settings-field-grid" :disabled="busy || scopedBusy || pendingDraftConflict">
        <div class="settings-field">
          <label for="portal-title">{{ translate('admin.portal_name') }}</label>
          <input v-if="editableSettings.has_draft" id="portal-title" :value="editableSettings.settings.title" readonly>
          <input v-else id="portal-title" ref="titleInput" v-model="detailsDraft.title" maxlength="200" required>
        </div>
        <div class="settings-field">
          <label for="portal-tagline">{{ translate('admin.tagline') }}</label>
          <input v-if="editableSettings.has_draft" id="portal-tagline" :value="editableSettings.settings.tagline" readonly>
          <input v-else id="portal-tagline" v-model="detailsDraft.tagline" maxlength="300">
        </div>
        <div class="settings-field">
          <label for="portal-description">{{ translate('admin.description') }}</label>
          <textarea v-if="editableSettings.has_draft" id="portal-description" :value="editableSettings.settings.description" readonly rows="4" />
          <textarea v-else id="portal-description" v-model="detailsDraft.description" maxlength="2000" rows="4" />
        </div>
        <div class="settings-field">
          <label for="portal-notice">{{ translate('admin.notice') }}</label>
          <textarea v-if="editableSettings.has_draft" id="portal-notice" :value="editableSettings.settings.notice" readonly rows="4" />
          <textarea v-else id="portal-notice" v-model="detailsDraft.notice" maxlength="2000" rows="4" />
        </div>
      </fieldset>
      <p v-if="editableSettings.has_draft">{{ translate('admin.settings_pending') }}</p>
    </form>

    <form v-if="editableSettings && page === 'languages' && localeDefaultsDraft" class="admin-domain domain--defaults" aria-labelledby="portal-defaults-heading" @submit.prevent="saveScoped('locale-defaults')">
      <AdminDomainHeader
        :title="translate('admin.settings_locale_defaults_heading')" heading-id="portal-defaults-heading" :save-label="translate('admin.settings_save_locale_defaults')"
        :disabled="!localeDefaultsDirty || publication.saveBlocked('locale-defaults')"
      />
      <PortalScopeFeedback v-bind="publication.feedback('locale-defaults')" @reload="refreshScoped('locale-defaults')" />
      <fieldset class="settings-fields settings-field-grid" :disabled="busy || scopedBusy || editableSettings.has_draft || pendingDraftConflict">
        <div class="settings-field">
          <label for="portal-default-language">{{ translate('admin.default_language') }}</label>
          <select id="portal-default-language" ref="defaultLanguageInput" v-model="localeDefaultsDraft.default_language" required>
            <option v-for="item in editableSettings.languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
              {{ item.native_name }}
            </option>
          </select>
        </div>
        <div class="settings-field">
          <label for="portal-fallback-language">{{ translate('admin.fallback_language') }}</label>
          <select id="portal-fallback-language" v-model="localeDefaultsDraft.fallback_language" required>
            <option v-for="item in catalogLanguages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
              {{ item.native_name }}
            </option>
          </select>
        </div>
      </fieldset>
    </form>

    <form v-if="editableSettings && page === 'languages'" class="admin-domain domain--rules" @submit.prevent="saveScoped('slug-rules')">
      <AdminDomainHeader
        :title="translate('admin.slug_rules_title')" heading-id="portal-slug-title" :save-label="translate('admin.settings_save_slug_rules')"
        :disabled="!slugRulesDirty || publication.saveBlocked('slug-rules')"
      />
      <PortalScopeFeedback v-bind="publication.feedback('slug-rules')" @reload="refreshScoped('slug-rules')" />
      <fieldset class="settings-fields" :disabled="busy || scopedBusy || editableSettings.has_draft || pendingDraftConflict">
        <PortalSlugRules
          ref="slugRules" :rules="slugRulesDraft" :languages="editableSettings.languages"
          :default-language="editableSettings.settings.default_language" :translate="translate" @update:rules="updateSlugRules"
        />
      </fieldset>
    </form>

    <form v-if="editableSettings" v-show="page === 'translations'" class="admin-domain" @submit.prevent="saveScoped('catalogs')">
      <AdminDomainHeader
        v-if="!registryRepairRequired && !editableSettings.has_draft"
        :title="localeLabel(catalogTarget || selectedLanguage)" :save-label="translate('admin.settings_save_translations')"
        :disabled="!catalogDrafts.hasCatalogEdit(catalogTarget || selectedLanguage) || publication.saveBlocked('catalogs')"
      />
      <PortalScopeFeedback v-bind="publication.feedback('catalogs')" @reload="refreshScoped('catalogs')" />
      <div class="settings-actions">
        <button v-if="registryRepairRequired && !registryKeysReloaded" type="button" :disabled="scopedBusy" @click="publication.reloadCatalogRegistry(loadScopeEditors)">
          {{ translate('admin.settings_catalog_reload_keys') }}
        </button>
        <button v-if="registryKeysReloaded" type="button" :disabled="busy || scopedBusy || conflict" @click="save">
          {{ translate('admin.settings_catalog_repair') }}
        </button>
        <button v-if="editableSettings.has_draft && dirty" type="button" :disabled="busy || scopedBusy || conflict || pendingDraftConflict" @click="save">
          {{ translate('admin.settings_apply_pending') }}
        </button>
      </div>
      <fieldset class="settings-translations" :disabled="busy || scopedBusy || pendingDraftConflict">
        <PortalTranslationSettings
          ref="translations" v-model:language="selectedLanguage" :languages="editableSettings.languages" :keys="editableSettings.message_keys"
          :messages="messages" :reference="reference" :reference-language="editableSettings.settings.fallback_language"
          :ui-language="uiLanguage" :ui-messages="uiMessages" :disabled-catalogs="disabledCatalogCodes" :translate="translate"
          :permission-texts="permissionTexts" @update:permission-texts="emit('update:permission-texts', $event)"
          @add-catalog="catalogDrafts.addCatalog()" @remove-catalog="catalogDrafts.removeCatalog($event)"
          @update:message="(key, value) => catalogDrafts.changeMessage(key, value)"
        />
      </fieldset>
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

.portal-settings--languages {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, max(26rem, calc((100% - .75rem) / 2))), 1fr));
}

.portal-settings--languages > :not(.domain--flags, .domain--defaults) {
  grid-column: 1 / -1;
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
  padding: 0;
  border: 0;
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
