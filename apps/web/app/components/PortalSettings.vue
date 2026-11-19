<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { AdminSettingsError, getPortalSettings, publishPortalSettings, savePortalSettings } from '~/utils/admin-site-settings'
import type { EditablePortal } from '~/utils/admin-site-settings'

const props = defineProps<{
  csrf: string
  translate: (key: string) => string
}>()
const emit = defineEmits<{
  published: []
  'editing-state': [state: { dirty: boolean, busy: boolean }]
  'step-up': []
  'session-changed': []
  close: []
}>()

const draft = ref<EditablePortal | null>(null)
const selectedLanguage = ref('')
const heading = ref<HTMLElement | null>(null)
const confirmationHeading = ref<HTMLElement | null>(null)
const publishButton = ref<HTMLButtonElement | null>(null)
const publicationStatus = ref<HTMLOutputElement | null>(null)
const busy = ref(false)
const dirty = ref(false)
watch([() => dirty.value, busy], ([edited, saving]) => {
  emit('editing-state', { dirty: edited, busy: saving })
})
const confirming = ref(false)
const conflict = ref(false)
const failure = ref('')
const notice = ref('')
let active = true

const language = computed(() => draft.value?.languages.find(item => item.code === selectedLanguage.value))
const catalogsByLanguage = computed(() => new Map(Object.entries(draft.value?.catalogs ?? {})))
const messages = computed(() => catalogsByLanguage.value.get(selectedLanguage.value))
const catalogLanguages = computed(() => {
  const editable = draft.value
  return editable?.languages.filter(item => catalogsByLanguage.value.has(item.code)) ?? []
})
const disabledCatalogCodes = computed(() => {
  const editable = draft.value
  if (!editable) return []
  const enabled = new Set(editable.languages.map(item => item.code))
  return Object.keys(editable.catalogs).filter(code => !enabled.has(code))
})
const publishDisabled = computed(() => busy.value || conflict.value || confirming.value || dirty.value || !draft.value?.has_draft)

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
    errorMessage('admin.step_up_required')
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
      draft.value = null
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
    draft.value = editable
    const preferred = editable.settings.fallback_language
    selectedLanguage.value = editable.languages.some(item => item.code === preferred)
      ? preferred : (editable.languages[0]?.code ?? '')
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
  const editable = draft.value
  if (!editable || !language.value || messages.value) return
  editable.catalogs[language.value.code] = {}
  dirty.value = true
}

function removeCatalog(code: string) {
  const editable = draft.value
  if (!editable || code === editable.settings.fallback_language) return
  editable.catalogs = Object.fromEntries(Object.entries(editable.catalogs).filter(([locale]) => locale !== code))
  dirty.value = true
}

async function save() {
  const editable = draft.value
  if (!editable || busy.value || conflict.value || confirming.value || !dirty.value) return
  busy.value = true
  failure.value = ''
  notice.value = ''
  try {
    const version = await savePortalSettings(props.csrf, {
      expected_version: editable.version,
      settings: editable.settings,
      catalogs: draftCatalogs(editable.catalogs),
    })
    if (!active) return
    editable.version = version
    editable.has_draft = true
    dirty.value = false
    notice.value = 'admin.draft_saved'
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    busy.value = false
  }
}

function draftCatalogs(catalogs: EditablePortal['catalogs']): EditablePortal['catalogs'] {
  return Object.fromEntries(Object.entries(catalogs).map(([code, entries]) => [
    code,
    Object.fromEntries(Object.entries(entries).filter(([, text]) => text.trim() !== '')),
  ]))
}

function incompleteCatalog(editable: EditablePortal): string | undefined {
  return Object.entries(editable.catalogs).find(([, entries]) => {
    const nonBlankKeys = new Set(Object.entries(entries).filter(([, text]) => text.trim()).map(([key]) => key))
    return editable.message_keys.some(key => !nonBlankKeys.has(key))
  })?.[0]
}

async function confirmPublication() {
  if (publishDisabled.value || !draft.value) return
  const incomplete = incompleteCatalog(draft.value)
  if (incomplete) {
    selectedLanguage.value = incomplete
    errorMessage('admin.settings_invalid')
    return
  }
  confirming.value = true
  await nextTick()
  confirmationHeading.value?.focus()
}

async function cancelPublication() {
  confirming.value = false
  await nextTick()
  publishButton.value?.focus()
}

async function publish() {
  const editable = draft.value
  if (!editable?.has_draft || busy.value || conflict.value || !confirming.value || dirty.value) return
  busy.value = true
  failure.value = ''
  notice.value = ''
  try {
    const version = await publishPortalSettings(props.csrf, editable.version)
    if (!active) return
    editable.version = version
    editable.has_draft = false
    notice.value = 'admin.published'
    emit('published')
    await nextTick()
    publicationStatus.value?.focus()
  }
  catch (error: unknown) {
    if (active) handleFailure(error)
  }
  finally {
    busy.value = false
    confirming.value = false
  }
}

onMounted(async () => {
  heading.value?.focus()
  await load()
})
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="portal-settings" aria-labelledby="portal-settings-heading" :aria-busy="busy">
    <div class="settings-header">
      <h2 id="portal-settings-heading" ref="heading" tabindex="-1">{{ translate('admin.settings') }}</h2>
      <button type="button" :disabled="busy" @click="emit('close')">{{ translate('admin.close') }}</button>
    </div>
    <output v-if="notice" ref="publicationStatus" tabindex="-1">{{ translate(notice) }}</output>
    <output v-if="busy">{{ translate('admin.working') }}</output>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <button v-if="conflict" type="button" :disabled="busy" @click="load">{{ translate('admin.reload_settings') }}</button>
    <button v-else-if="!draft && failure" type="button" :disabled="busy" @click="load">{{ translate('admin.retry') }}</button>

    <form v-if="draft" @submit.prevent="save">
      <fieldset class="settings-fields" :disabled="busy || confirming" @input="dirty = true">
        <label for="portal-title">{{ translate('admin.portal_name') }}</label>
        <input id="portal-title" v-model="draft.settings.title" maxlength="200" required>

        <label for="portal-tagline">{{ translate('admin.tagline') }}</label>
        <input id="portal-tagline" v-model="draft.settings.tagline" maxlength="300">

        <label for="portal-description">{{ translate('admin.description') }}</label>
        <textarea id="portal-description" v-model="draft.settings.description" maxlength="2000" rows="4" />

        <label for="portal-notice">{{ translate('admin.notice') }}</label>
        <textarea id="portal-notice" v-model="draft.settings.notice" maxlength="2000" rows="3" />

        <label for="portal-accent">{{ translate('admin.accent') }}</label>
        <input id="portal-accent" v-model="draft.settings.accent" pattern="#[0-9a-fA-F]{6}" required>

        <label for="portal-default-language">{{ translate('admin.default_language') }}</label>
        <select id="portal-default-language" v-model="draft.settings.default_language" required>
          <option v-for="item in draft.languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
            {{ item.native_name }}
          </option>
        </select>

        <label for="portal-fallback-language">{{ translate('admin.fallback_language') }}</label>
        <select id="portal-fallback-language" v-model="draft.settings.fallback_language" required>
          <option v-for="item in catalogLanguages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
            {{ item.native_name }}
          </option>
        </select>
      </fieldset>

      <fieldset class="settings-fields" :disabled="busy || confirming">
        <legend>{{ translate('admin.interface_texts') }}</legend>
        <label for="portal-catalog-language">{{ translate('admin.language') }}</label>
        <select id="portal-catalog-language" v-model="selectedLanguage">
          <option v-for="item in draft.languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
            {{ item.native_name }}
          </option>
        </select>
        <button v-if="language && !messages" type="button" @click="addCatalog">{{ translate('admin.add_catalog') }}</button>
        <button
          v-if="language && messages && language.code !== draft.settings.fallback_language"
          type="button" @click="removeCatalog(language.code)"
        >{{ translate('admin.remove_catalog') }}</button>
        <template v-for="code in disabledCatalogCodes" :key="code">
          <button v-if="code !== draft.settings.fallback_language" type="button" @click="removeCatalog(code)">
            {{ translate('admin.remove_catalog') }} ({{ code }})
          </button>
        </template>
        <template v-if="messages">
          <template v-for="(key, index) in draft.message_keys" :key="key">
            <label class="message-key" :for="`portal-message-${index}`"><code>{{ key }}</code></label>
            <textarea
              :id="`portal-message-${index}`"
              v-model="messages[key]"
              :lang="language?.code"
              :dir="language?.direction"
              rows="2"
              maxlength="4000"
              @input="dirty = true"
            />
          </template>
        </template>
      </fieldset>

      <div class="settings-actions">
        <button type="submit" :disabled="busy || confirming || conflict || !dirty">{{ translate('admin.save_draft') }}</button>
        <button ref="publishButton" type="button" :disabled="publishDisabled" @click="confirmPublication">
          {{ translate('admin.publish_draft') }}
        </button>
      </div>
    </form>
    <section v-if="confirming && draft" class="settings-confirmation" aria-labelledby="publication-confirmation-heading">
      <h3 id="publication-confirmation-heading" ref="confirmationHeading" tabindex="-1">{{ translate('admin.confirm_publish') }}</h3>
      <dl>
        <dt>{{ translate('admin.portal_name') }}</dt>
        <dd>{{ draft.settings.title }}</dd>
        <dt>{{ translate('admin.interface_texts') }}</dt>
        <dd>{{ Object.keys(draft.catalogs).length }}</dd>
      </dl>
      <div class="settings-actions">
        <button type="button" :disabled="busy" @click="publish">{{ translate('admin.publish_draft') }}</button>
        <button type="button" :disabled="busy" @click="cancelPublication">{{ translate('admin.cancel') }}</button>
      </div>
    </section>
  </section>
</template>

<style scoped>
.portal-settings {
  display: grid;
  gap: 1rem;
  min-inline-size: 0;
}

.settings-header,
.settings-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
}

.settings-header {
  justify-content: space-between;
}

.settings-header h2,
.settings-confirmation h3,
.settings-confirmation dl,
.settings-confirmation dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.portal-settings form,
.settings-fields {
  display: grid;
  gap: 0.65rem;
  min-inline-size: 0;
}

.settings-fields {
  margin: 0;
  padding: 0.75rem;
  border: 1px solid var(--admin-border);
  border-radius: 0.4rem;
}

.portal-settings input,
.portal-settings select,
.portal-settings textarea,
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

.message-key {
  overflow-wrap: anywhere;
}

.settings-confirmation {
  display: grid;
  gap: 1rem;
  padding: 1rem;
  border: 1px solid var(--admin-border);
  border-radius: 0.4rem;
}
</style>
