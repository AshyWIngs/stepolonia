<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { AdminSettingsError, getPortalSettings, savePortalSettings, saveScopedPortalSetting } from '~/utils/admin-site-settings'
import type { EditablePortal, PortalDetails, PortalFields, PortalLocaleDefaults, PortalSettingsPage, ScopedSettingsInput, ScopedSettingsSection } from '~/utils/admin-site-settings'
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
const translations = ref<{ showMissing: () => Promise<void>, focusActive: () => Promise<void> }>()
const slugRules = ref<{ showInvalid: (index: number) => Promise<void>, focusActive: () => Promise<void> }>()
const flagEditor = ref<{ focusInvalid: () => Promise<void> }>()
const settingsPanel = ref<HTMLDivElement>()
const accentInput = ref<HTMLInputElement>()
const titleInput = ref<HTMLInputElement>()
const defaultLanguageInput = ref<HTMLSelectElement>()
const detailsDraft = ref<PortalDetails>()
const detailsBase = ref<PortalDetails>()
const localeDefaultsDraft = ref<PortalLocaleDefaults>()
const localeDefaultsBase = ref<PortalLocaleDefaults>()
const slugRulesDraft = ref<SlugRule[]>([])
const flagUpdates = ref<Record<string, string>>({})
const flagLanguage = ref('')
const accentDraft = ref('')
const scopedBusy = ref(false)
const scopedNotice = ref('')
const scopedFailure = ref('')
const catalogEdits = ref(new Map<string, Record<string, string> | null>())
const catalogBases = ref(new Map<string, Record<string, string> | undefined>())
const catalogTarget = ref('')
const registryRepairRequired = ref(false)
const registryKeysReloaded = ref(false)
const savedValueDifferences = ref(new Map<string, string[]>())
const conflictedScopes = ref(new Set<string>())
const retryTimers = new Map<string, ReturnType<typeof setTimeout>>()
const rateLimitedScopes = ref(new Set<string>())
const reportedScope = ref<ScopedSettingsSection>(pageScope(props.page))
function scopeKey(scope: ScopedSettingsSection) {
  if (scope === 'language-flags') return `flag:${flagLanguage.value}`
  if (scope === 'catalogs') return `catalog:${catalogTarget.value || selectedLanguage.value}`
  return scope
}
function hasScopeConflict(scope: ScopedSettingsSection) {
  return conflictedScopes.value.has(scopeKey(scope))
}
function scopeSaveBlocked(scope: ScopedSettingsSection) {
  return busy.value || scopedBusy.value || pendingDraftConflict.value || editableSettings.value?.has_draft ||
    hasScopeConflict(scope) || rateLimitedScopes.value.has(scopeKey(scope))
}
const scopedConflict = computed(() => hasScopeConflict(reportedScope.value))
const changedSavedValues = computed(() => registryKeysReloaded.value
  ? [...savedValueDifferences.value].filter(([scope]) => scope.startsWith('catalog:')).flatMap(([, values]) => values)
  : savedValueDifferences.value.get(scopeKey(reportedScope.value)) ?? [])
const pendingDraftConflict = ref(false)
const publicationStatus = ref<HTMLOutputElement | null>(null)
const busy = ref(false)
const dirty = computed(() => catalogEdits.value.size > 0)
const detailsDirty = computed(() => JSON.stringify(detailsDraft.value) !== JSON.stringify(detailsBase.value))
const localeDefaultsDirty = computed(() => JSON.stringify(localeDefaultsDraft.value) !== JSON.stringify(localeDefaultsBase.value))
const slugRulesDirty = computed(() => JSON.stringify(slugRulesDraft.value) !== JSON.stringify(editableSettings.value?.slug_rules ?? []))
const hasEdits = computed(() => dirty.value || detailsDirty.value || localeDefaultsDirty.value ||
  slugRulesDirty.value || accentDraft.value !== (editableSettings.value?.settings.accent ?? '') || Object.keys(flagUpdates.value).length > 0)
watch([hasEdits, () => busy.value || scopedBusy.value], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const conflict = ref(false)
const failure = ref('')
const notice = ref('')
let active = true

const language = computed(() => editableSettings.value?.languages.find(item => item.code === selectedLanguage.value))
const catalogsByLanguage = computed(() => {
  const catalogs = new Map(Object.entries(editableSettings.value?.catalogs ?? {}))
  for (const [code, entries] of catalogEdits.value) {
    if (entries) catalogs.set(code, entries)
    else catalogs.delete(code)
  }
  return catalogs
})
const messages = computed(() => catalogsByLanguage.value.get(selectedLanguage.value))
const reference = computed(() => catalogsByLanguage.value.get(editableSettings.value?.settings.fallback_language ?? ''))
const catalogLanguages = computed(() => {
  const editable = editableSettings.value
  return editable?.languages.filter(item => Object.hasOwn(editable.catalogs, item.code)) ?? []
})
const disabledCatalogCodes = computed(() => {
  const editable = editableSettings.value
  if (!editable) return []
  const enabled = new Set(editable.languages.map(item => item.code))
  return [...catalogsByLanguage.value.keys()].filter(code => !enabled.has(code) && code !== editable.settings.fallback_language)
})
const accentPattern = /^#[\da-f]{6}$/iu
const pickerColor = computed(() => {
  const accent = accentDraft.value
  return accent && accentPattern.test(accent) ? accent : undefined
})

function errorMessage(key: string) {
  failure.value = key
}

function portalDetails({ title, tagline, description, notice }: PortalFields): PortalDetails {
  return { title, tagline, description, notice }
}

function portalLocaleDefaults({ default_language, fallback_language }: PortalFields): PortalLocaleDefaults {
  return { default_language, fallback_language }
}

function pageScope(page: PortalSettingsPage): ScopedSettingsSection {
  if (page === 'identity') return 'details'
  if (page === 'translations') return 'catalogs'
  return page === 'appearance' ? 'appearance' : 'language-flags'
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
      clearCatalogEdits()
      errorMessage('admin.access_denied')
      break
    case 409:
      conflict.value = true
      errorMessage('admin.settings_conflict')
      break
    case 429:
      errorMessage('admin.limited')
      break
    default:
      errorMessage('admin.unavailable')
  }
}

async function load() {
  if (busy.value || scopedBusy.value) return
  busy.value = true
  failure.value = ''
  notice.value = ''
  try {
    const editable = await getPortalSettings()
    if (!active) return
    loadScopeEditors(editable)
    editableSettings.value = editable
    pendingDraftConflict.value = false
    selectedLanguage.value = editable.languages.some(item => item.code === props.uiLanguage)
      ? props.uiLanguage : editable.settings.fallback_language
    clearCatalogEdits()
    conflict.value = false
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    busy.value = false
  }
}

function loadScopeEditors(editable: EditablePortal) {
  const previous = editableSettings.value
  if (previous && detailsDirty.value) editable.details_version = previous.details_version
  else {
    detailsBase.value = portalDetails(editable.settings)
    detailsDraft.value = { ...detailsBase.value }
  }
  if (previous && localeDefaultsDirty.value) editable.locale_defaults_version = previous.locale_defaults_version
  else {
    localeDefaultsBase.value = portalLocaleDefaults(editable.settings)
    localeDefaultsDraft.value = { ...localeDefaultsBase.value }
  }
  if (previous && slugRulesDirty.value) {
    editable.slug_rules_version = previous.slug_rules_version
    editable.slug_rules = previous.slug_rules
  } else {
    slugRulesDraft.value = editable.slug_rules
  }
  for (const code of Object.keys(flagUpdates.value)) {
    const version = previous?.language_flag_versions[code]
    if (version) editable.language_flag_versions[code] = version
  }
  if (!previous || accentDraft.value === previous.settings.accent) accentDraft.value = editable.settings.accent
  else editable.appearance_version = previous.appearance_version
}

function addCatalog() {
  const editable = editableSettings.value
  if (!editable || !language.value || messages.value) return
  stageCatalogEdit(editable, language.value.code, {})
}

function removeCatalog(code: string) {
  const editable = editableSettings.value
  if (!editable || code === editable.settings.fallback_language) return
  if (!Object.hasOwn(editable.catalogs, code)) {
    clearCatalogEdit(code)
    return
  }
  stageCatalogEdit(editable, code, null)
  catalogTarget.value = code
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
  const code = selectedLanguage.value
  const next = Object.fromEntries(updated)
  if (Object.keys(changedMessages(catalogBase(editable, code), next)).length > 0) stageCatalogEdit(editable, code, next)
  else clearCatalogEdit(code)
}

function catalogBase(editable: EditablePortal, code: string) {
  return catalogBases.value.has(code) ? catalogBases.value.get(code) : editable.catalogs[code]
}

function stageCatalogEdit(editable: EditablePortal, code: string, values: Record<string, string> | null) {
  if (!catalogBases.value.has(code)) catalogBases.value.set(code, editable.catalogs[code])
  catalogEdits.value.set(code, values)
}

function clearCatalogEdit(code: string) {
  catalogEdits.value.delete(code)
  catalogBases.value.delete(code)
}

function clearCatalogEdits() {
  catalogEdits.value.clear()
  catalogBases.value.clear()
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

watch([() => props.page, flagLanguage, selectedLanguage], () => {
  reportedScope.value = pageScope(props.page)
  catalogTarget.value = ''
  scopedNotice.value = ''
  scopedFailure.value = ''
})

async function saveScoped(scope: ScopedSettingsSection) {
  const editable = editableSettings.value
  if (!editable || scopeSaveBlocked(scope)) return
  reportedScope.value = scope
  const code = scope === 'catalogs' ? catalogTarget.value || selectedLanguage.value : flagLanguage.value
  const input = await validatedScopedInput(editable, scope, code)
  if (!input) return
  scopedBusy.value = true
  notice.value = ''
  scopedNotice.value = ''
  scopedFailure.value = ''
  try {
    const result = await saveScopedPortalSetting(scope, props.csrf, input)
    if (!active) return
    commitScopedResult(editable, scope, code, result)
    scopedNotice.value = 'admin.settings_saved'
    emit('published')
  }
  catch (error: unknown) {
    if (active) showScopedFailure(error)
  }
  finally { scopedBusy.value = false }
  await focusScopedResult(scope)
}

async function focusScopedResult(scope: ScopedSettingsSection) {
  if (!active || pendingDraftConflict.value || scopedFailure.value === 'admin.changed') return
  if (hasScopeConflict(scope)) {
    await nextTick()
    settingsPanel.value?.querySelector<HTMLButtonElement>(`[data-reload-scope="${scope}"]`)?.focus()
  } else if (scopedFailure.value) {
    await focusScopeControl(scope)
  } else if (scopedNotice.value) {
    await nextTick()
    settingsPanel.value?.querySelector<HTMLOutputElement>(`[data-saved-scope="${scope}"]`)?.focus()
  }
}

function scopeFeedback(scope: ScopedSettingsSection) {
  const reported = reportedScope.value === scope
  const conflicted = hasScopeConflict(scope)
  const error = (reported && scopedFailure.value) || scopeBlockingMessage(scope, conflicted)
  return {
    scope,
    status: reported && scopedNotice.value ? props.translate(scopedNotice.value) : '',
    error: error ? props.translate(error) : '',
    reload: conflicted && !pendingDraftConflict.value && !editableSettings.value?.has_draft ? props.translate('admin.settings_scoped_reload') : '',
    review: props.translate('admin.settings_review_saved_values'),
    values: reported ? changedSavedValues.value : [],
    disabled: scopedBusy.value,
  }
}

function scopeBlockingMessage(scope: ScopedSettingsSection, conflicted: boolean) {
  if (conflicted) return 'admin.settings_conflict'
  return rateLimitedScopes.value.has(scopeKey(scope)) ? 'admin.limited' : ''
}

async function validatedScopedInput(editable: EditablePortal, scope: ScopedSettingsSection, code: string) {
  if (scope === 'slug-rules' && !await validateSlugRulesDraft()) return
  if (scope === 'catalogs' && !await canSaveCatalog(editable, code)) return
  return scopedInput(editable, scope, code)
}

function scopedInput(editable: EditablePortal, scope: ScopedSettingsSection, code: string): ScopedSettingsInput | undefined {
  if (scope === 'details') return detailsDraft.value && { expected_version: editable.details_version, details: detailsDraft.value }
  if (scope === 'appearance') return { expected_version: editable.appearance_version, accent: accentDraft.value }
  if (scope === 'locale-defaults') return localeDefaultsDraft.value && { expected_version: editable.locale_defaults_version, defaults: localeDefaultsDraft.value }
  if (scope === 'slug-rules') return slugRulesInput(editable)
  if (scope === 'catalogs') return catalogInput(editable, code)
  const svg = flagUpdates.value[code]
  if (svg !== undefined) return { expected_version: editable.language_flag_versions[code] ?? '', language_code: code, svg }
}

function changedMessages(saved: Record<string, string> | undefined, edited: Record<string, string>) {
  return Object.fromEntries(Object.entries(edited).filter(([key, value]) => saved?.[key] !== value))
}

function catalogInput(editable: EditablePortal, code: string) {
  const edit = catalogEdits.value.get(code)
  if (edit === undefined) return
  const target = { expected_version: editable.catalog_versions[code] ?? '', language_code: code }
  if (edit === null) return { ...target, remove_catalog: true as const }
  const messages = changedMessages(catalogBase(editable, code), edit)
  if (Object.keys(messages).length > 0) return { ...target, messages }
  clearCatalogEdit(code)
}

async function canSaveCatalog(editable: EditablePortal, code: string) {
  const edit = catalogEdits.value.get(code)
  if (edit === null) return true
  if (!edit) return false
  if (editable.message_keys.every(key => edit[key]?.trim())) return true
  scopedNotice.value = ''
  scopedFailure.value = 'admin.settings_complete_catalog'
  await translations.value?.showMissing()
  return false
}

function normalizedDraftRules() {
  return slugRulesDraft.value.map(rule => ({ ...rule, character: normalizeSlugCharacter(rule.character) }))
}

function slugRulesInput(editable: EditablePortal) {
  const delta = changedSlugRules(editable.slug_rules, normalizedDraftRules())
  if (delta.upsert || delta.remove) return { expected_version: editable.slug_rules_version, ...delta }
  slugRulesDraft.value = editable.slug_rules
}

function slugRuleKey(rule: Pick<SlugRule, 'language_code' | 'character'>) {
  return JSON.stringify([rule.language_code, rule.character])
}

function changedSlugRules(saved: SlugRule[], next: SlugRule[]) {
  const previous = new Map(saved.map(rule => [slugRuleKey(rule), rule]))
  const upsert = next.filter((rule) => {
    const key = slugRuleKey(rule)
    const before = previous.get(key)
    previous.delete(key)
    return before?.replacement !== rule.replacement
  })
  const remove = [...previous.values()].map(({ language_code, character }) => ({ language_code, character }))
  return { upsert: upsert.length ? upsert : undefined, remove: remove.length ? remove : undefined }
}

function rebaseSlugRules(before: SlugRule[], fresh: SlugRule[]) {
  const { upsert, remove } = changedSlugRules(before, normalizedDraftRules())
  const merged = new Map(fresh.map(rule => [slugRuleKey(rule), rule]))
  const prior = new Map(before.map(rule => [slugRuleKey(rule), rule]))
  savedValueDifferences.value.set('slug-rules', [...(upsert ?? []), ...(remove ?? [])].flatMap((rule) => {
    const key = slugRuleKey(rule)
    const current = merged.get(key)
    return prior.get(key)?.replacement !== current?.replacement
      ? [`${localeLabel(rule.language_code)} · ${rule.character} → ${current?.replacement ?? props.translate('admin.settings_removed_value')}`] : []
  }))
  for (const key of remove ?? []) merged.delete(slugRuleKey(key))
  for (const rule of upsert ?? []) merged.set(slugRuleKey(rule), rule)
  slugRulesDraft.value = [...merged.values()]
}

async function validateSlugRulesDraft() {
  const index = invalidSlugRuleIndex(slugRulesDraft.value)
  if (index < 0) return true
  scopedNotice.value = ''
  scopedFailure.value = 'admin.slug_rules_invalid'
  await slugRules.value?.showInvalid(index)
  return false
}

function commitScopedResult(
  editable: EditablePortal,
  scope: ScopedSettingsSection,
  code: string,
  result: Awaited<ReturnType<typeof saveScopedPortalSetting>>,
) {
  if (result.previousVersion === editable.version) editable.version = result.version
  editable.published_id = result.publicationID
  savedValueDifferences.value.delete(scopeKey(scope))
  if (scope === 'details') {
    Object.assign(editable.settings, detailsDraft.value)
    detailsBase.value = portalDetails(editable.settings)
    editable.details_version = result.scopeVersion
  } else if (scope === 'locale-defaults') {
    Object.assign(editable.settings, localeDefaultsDraft.value)
    localeDefaultsBase.value = portalLocaleDefaults(editable.settings)
    editable.locale_defaults_version = result.scopeVersion
  } else if (scope === 'slug-rules') {
    editable.slug_rules = normalizedDraftRules()
    slugRulesDraft.value = editable.slug_rules
    editable.slug_rules_version = result.scopeVersion
  } else if (scope === 'catalogs') {
    commitCatalog(editable, code, result.scopeVersion)
  } else if (scope === 'appearance') {
    editable.settings.accent = result.value
    editable.appearance_version = result.scopeVersion
    accentDraft.value = result.value
  } else {
    editable.language_flags = { ...editable.language_flags, [code]: result.value }
    editable.language_flag_versions[code] = result.scopeVersion
    flagUpdates.value = Object.fromEntries(Object.entries(flagUpdates.value).filter(([language]) => language !== code))
  }
}

function commitCatalog(editable: EditablePortal, code: string, version: string) {
  const catalogs = new Map(Object.entries(editable.catalogs))
  const entries = catalogEdits.value.get(code)
  if (entries) catalogs.set(code, { ...editable.catalogs[code], ...changedMessages(catalogBase(editable, code), entries) })
  else catalogs.delete(code)
  editable.catalogs = Object.fromEntries(catalogs)
  editable.catalog_versions[code] = version
  clearCatalogEdit(code)
  catalogTarget.value = ''
}

function showScopedFailure(error: unknown) {
  if (!(error instanceof AdminSettingsError)) {
    scopedFailure.value = 'admin.unavailable'
    return
  }
  if (error.status === 429) {
    startScopedCooldown(error.retryAfter ?? 1)
    return
  }
  if (error.status === 401 || error.code === 'csrf_failed') {
    scopedFailure.value = 'admin.changed'
    emit('session-changed')
  } else if (error.code === 'invalid_language_flag') {
    scopedFailure.value = 'admin.language_flag_invalid'
  } else if (error.code === 'site_draft_pending') {
    pendingDraftConflict.value = true
  } else if (error.code === 'invalid_slug_rules') {
    scopedFailure.value = 'admin.slug_rules_invalid'
  } else if (error.code === 'catalog_registry_changed') {
    registryRepairRequired.value = true
    registryKeysReloaded.value = false
    scopedFailure.value = 'admin.settings_catalog_registry_changed'
  } else if (error.status === 409) {
    conflictedScopes.value.add(scopeKey(reportedScope.value))
  } else if (error.status === 400) {
    scopedFailure.value = scopedValidationMessage[reportedScope.value]
  } else if (error.status === 403) {
    scopedFailure.value = 'admin.access_denied'
  } else {
    scopedFailure.value = 'admin.unavailable'
  }
}

function startScopedCooldown(seconds: number) {
  const key = scopeKey(reportedScope.value)
  scopedFailure.value = 'admin.limited'
  rateLimitedScopes.value.add(key)
  clearTimeout(retryTimers.get(key))
  retryTimers.set(key, setTimeout(() => {
    retryTimers.delete(key)
    rateLimitedScopes.value.delete(key)
    if (key === scopeKey(reportedScope.value) && scopedFailure.value === 'admin.limited') scopedFailure.value = ''
  }, Math.min(seconds, 2147483) * 1000))
}

const scopedValidationMessage: Record<ScopedSettingsSection, string> = {
  details: 'admin.settings_details_invalid',
  appearance: 'admin.settings_accent_invalid',
  'language-flags': 'admin.language_flag_invalid',
  'locale-defaults': 'admin.settings_locale_defaults_invalid',
  'slug-rules': 'admin.slug_rules_invalid',
  catalogs: 'admin.settings_catalog_invalid',
}

async function refreshScoped(scope = reportedScope.value) {
  const editable = editableSettings.value
  if (!editable || scopedBusy.value) return
  reportedScope.value = scope
  scopedBusy.value = true
  try {
    const fresh = await getPortalSettings()
    if (!active) return
    editable.has_draft = fresh.has_draft
    if (fresh.has_draft) {
      pendingDraftConflict.value = true
      return
    }
    updateScopedBase(editable, fresh, scope)
    conflictedScopes.value.delete(scopeKey(scope))
    scopedFailure.value = ''
  }
  catch (error: unknown) { if (active) showScopedFailure(error) }
  finally { scopedBusy.value = false }
  await refocusScopedInput(scope)
}

function updateScopedBase(editable: EditablePortal, fresh: EditablePortal, scope: ScopedSettingsSection) {
  switch (scope) {
    case 'details':
      detailsDraft.value = rebaseFields(scope, detailsBase.value, detailsDraft.value, portalDetails(fresh.settings))
      detailsBase.value = portalDetails(fresh.settings)
      Object.assign(editable.settings, detailsBase.value)
      editable.details_version = fresh.details_version
      break
    case 'locale-defaults':
      localeDefaultsDraft.value = rebaseFields(scope, localeDefaultsBase.value, localeDefaultsDraft.value, portalLocaleDefaults(fresh.settings))
      localeDefaultsBase.value = portalLocaleDefaults(fresh.settings)
      Object.assign(editable.settings, localeDefaultsBase.value)
      editable.locale_defaults_version = fresh.locale_defaults_version
      break
    case 'slug-rules':
      rebaseSlugRules(editable.slug_rules, fresh.slug_rules)
      editable.slug_rules = fresh.slug_rules
      editable.slug_rules_version = fresh.slug_rules_version
      break
    case 'catalogs':
      rebaseCatalog(editable, fresh, catalogTarget.value || selectedLanguage.value)
      break
    case 'appearance':
      editable.appearance_version = fresh.appearance_version
      editable.settings.accent = fresh.settings.accent
      break
    default: {
      const code = flagLanguage.value
      editable.language_flags = { ...editable.language_flags, [code]: fresh.language_flags?.[code] ?? '' }
      editable.language_flag_versions[code] = fresh.language_flag_versions[code] ?? ''
    }
  }
}

function rebaseFields<T extends Record<string, string>>(scope: ScopedSettingsSection, before: T | undefined, pending: T | undefined, fresh: T): T {
  const edits = pending ? changedMessages(before, pending) : {}
  const overlapping = Object.keys(edits).filter(key => before?.[key] !== fresh[key])
  savedValueDifferences.value.set(scope, overlapping.map(key => fresh[key] ?? props.translate('admin.settings_removed_value')))
  return { ...fresh, ...edits }
}

function rebaseCatalog(editable: EditablePortal, fresh: EditablePortal, code: string) {
  const previous = catalogBase(editable, code)
  const actual = fresh.catalogs[code]
  const pending = catalogEdits.value.get(code)
  if (pending) {
    const delta = changedMessages(previous, pending)
    showCatalogDifferences(previous, actual, Object.keys(delta), code)
    if (Object.keys(changedMessages(actual, delta)).length > 0) {
      catalogEdits.value.set(code, { ...actual, ...delta })
      catalogBases.value.set(code, actual)
    } else clearCatalogEdit(code)
  } else if (pending === null && actual) {
    showCatalogDifferences(previous, actual, Object.keys({ ...previous, ...actual }), code)
    catalogBases.value.set(code, actual)
  } else if (!actual) {
    clearCatalogEdit(code)
  }
  const catalogs = new Map(Object.entries(editable.catalogs))
  if (actual) catalogs.set(code, actual)
  else catalogs.delete(code)
  editable.catalogs = Object.fromEntries(catalogs)
  editable.catalog_versions[code] = fresh.catalog_versions[code] ?? ''
}

function localeLabel(code: string) {
  return editableSettings.value?.languages.find(language => language.code === code)?.native_name ?? code
}

function showCatalogDifferences(before: Record<string, string> | undefined, actual: Record<string, string> | undefined, keys: string[], code: string) {
  const differences = keys.filter(key => before?.[key] !== actual?.[key]).map(key =>
    `${localeLabel(code)} · ${actual?.[key] ?? props.translate('admin.settings_removed_value')}`,
  )
  savedValueDifferences.value.set(`catalog:${code}`, differences)
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
  if (pendingDraftConflict.value && !dirty.value) await load()
  if (pendingDraftConflict.value) {
    conflict.value = true
    errorMessage('admin.settings_conflict')
  }
  await showPage('identity')
}

function invalidSettingsSection(editable: EditablePortal): PortalSettingsPage | undefined {
  const settings = editable.settings
  if (!settings.title.trim()) return 'identity'
  if (!accentPattern.test(settings.accent)) return 'appearance'
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
  const editable = editableSettings.value
  if (!editable || busy.value || scopedBusy.value || conflict.value || pendingDraftConflict.value || (!registryKeysReloaded.value && !editable.has_draft)) return
  const catalogs = Object.fromEntries(catalogsByLanguage.value)
  if (!await validateSettings(editable, catalogs)) return
  busy.value = true
  failure.value = ''
  notice.value = ''
  scopedNotice.value = ''
  try {
    const result = await savePortalSettings(props.csrf, {
      expected_version: editable.version,
      settings: editable.settings,
      catalogs,
    })
    if (!active) return
    editable.version = result.version
    editable.published_id = result.publicationID
    editable.has_draft = false
    editable.catalogs = catalogs
    editable.language_flags = { ...editable.language_flags, ...result.languageFlags }
    clearCatalogEdits()
    await refreshLegacyVersions(result.publicationID)
    registryKeysReloaded.value = false
    registryRepairRequired.value = false
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

async function refreshLegacyVersions(publicationID: string) {
  const fresh = await getPortalSettings().catch(() => undefined)
  if (!active || fresh?.published_id !== publicationID || !editableSettings.value) return
  loadScopeEditors(fresh)
  preserveCatalogVersions(fresh)
  editableSettings.value = fresh
}

function preserveCatalogVersions(fresh: EditablePortal) {
  const current = editableSettings.value
  if (!current) return
  for (const code of catalogEdits.value.keys()) {
    const version = current.catalog_versions[code]
    if (version) fresh.catalog_versions[code] = version
  }
}

async function reloadCatalogRegistry() {
  const current = editableSettings.value
  if (!current || scopedBusy.value || busy.value) return
  scopedBusy.value = true
  try {
    const fresh = await getPortalSettings()
    if (!active) return
    if (fresh.has_draft) {
      pendingDraftConflict.value = true
      return
    }
    for (const code of catalogEdits.value.keys()) {
      rebaseCatalog(current, fresh, code)
      const entries = catalogEdits.value.get(code)
      if (entries) catalogEdits.value.set(code, Object.fromEntries(Object.entries(entries).filter(([key]) => fresh.message_keys.includes(key))))
    }
    loadScopeEditors(fresh)
    editableSettings.value = fresh
    registryKeysReloaded.value = true
    scopedFailure.value = ''
  } catch (error: unknown) {
    if (active) showScopedFailure(error)
  } finally { scopedBusy.value = false }
}

function incompleteCatalog(catalogs: EditablePortal['catalogs'], keys: string[]): string | undefined {
  return Object.entries(catalogs).find(([, entries]) => {
    const nonBlankKeys = new Set(Object.entries(entries).filter(([, text]) => text.trim()).map(([key]) => key))
    return keys.some(key => !nonBlankKeys.has(key))
  })?.[0]
}

onMounted(async () => {
  await load()
})
onBeforeUnmount(() => {
  active = false
  retryTimers.forEach(timer => clearTimeout(timer))
})
</script>

<template>
  <div ref="settingsPanel" class="portal-settings" :class="{ 'portal-settings--languages': page === 'languages' }" :aria-busy="busy || scopedBusy">
    <output v-if="notice" ref="publicationStatus" tabindex="-1">{{ translate(notice) }}</output>
    <output v-if="busy">{{ translate('admin.working') }}</output>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <button v-if="conflict" type="button" :disabled="busy || scopedBusy" @click="load">{{ translate('admin.reload_settings') }}</button>
    <button v-else-if="!editableSettings && failure" type="button" :disabled="busy || scopedBusy" @click="load">{{ translate('admin.retry') }}</button>

    <template v-if="page !== 'identity' && (editableSettings?.has_draft || pendingDraftConflict)">
      <p><output>{{ translate('admin.settings_scoped_pending') }}</output></p>
      <div><button type="button" :disabled="busy || scopedBusy" @click="reviewPendingDraft">{{ translate('admin.settings_identity') }}</button></div>
    </template>

    <form v-if="editableSettings && page === 'appearance'" class="admin-domain" @submit.prevent="saveScoped('appearance')">
      <AdminDomainHeader
        :save-label="translate('admin.settings_save_appearance')"
        :disabled="scopeSaveBlocked('appearance') || accentDraft === editableSettings.settings.accent"
      />
      <PortalScopeFeedback v-bind="scopeFeedback('appearance')" @reload="refreshScoped('appearance')" />
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

    <form v-if="editableSettings" v-show="page === 'languages'" class="admin-domain domain--flags" aria-labelledby="portal-flags-heading" @submit.prevent="saveScoped('language-flags')">
      <AdminDomainHeader
        :title="translate('admin.language_flags')" heading-id="portal-flags-heading" :save-label="translate('admin.language_flag_save')"
        :disabled="scopeSaveBlocked('language-flags') || !Object.hasOwn(flagUpdates, flagLanguage)"
      />
      <PortalScopeFeedback v-bind="scopeFeedback('language-flags')" @reload="refreshScoped('language-flags')" />
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
        :disabled="!detailsDirty || scopeSaveBlocked('details') || conflict"
      />
      <div v-else class="settings-actions">
        <button type="submit" class="settings-primary" :disabled="busy || scopedBusy || conflict || pendingDraftConflict">
          {{ translate('admin.settings_apply_pending') }}
        </button>
      </div>
      <PortalScopeFeedback v-bind="scopeFeedback('details')" @reload="refreshScoped('details')" />
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
        :disabled="!localeDefaultsDirty || scopeSaveBlocked('locale-defaults')"
      />
      <PortalScopeFeedback v-bind="scopeFeedback('locale-defaults')" @reload="refreshScoped('locale-defaults')" />
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
        :disabled="!slugRulesDirty || scopeSaveBlocked('slug-rules')"
      />
      <PortalScopeFeedback v-bind="scopeFeedback('slug-rules')" @reload="refreshScoped('slug-rules')" />
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
        :disabled="!catalogEdits.has(catalogTarget || selectedLanguage) || scopeSaveBlocked('catalogs')"
      />
      <PortalScopeFeedback v-bind="scopeFeedback('catalogs')" @reload="refreshScoped('catalogs')" />
      <div class="settings-actions">
        <button v-if="registryRepairRequired && !registryKeysReloaded" type="button" :disabled="scopedBusy" @click="reloadCatalogRegistry">
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
          @add-catalog="addCatalog" @remove-catalog="removeCatalog" @update:message="changeMessage"
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
