<script setup lang="ts">
import { computed } from 'vue'
import type { CollectionChoice, FieldKind, RegistryField } from '~/utils/admin-content'
import type { DisplayPart, FieldEditorText, TranslationLanguage } from '~/features/content-registry/useRegistryFieldTranslations'

defineProps<{
  rowId: number
  locked: boolean
  existing: boolean
  busy: boolean
  targets: CollectionChoice[]
  collectionLabel: (entry: CollectionChoice) => string
  language?: TranslationLanguage
  text: FieldEditorText
  translate: (key: string) => string
}>()
const emit = defineEmits<{ translation: [part: DisplayPart, value: string], remove: [] }>()
const field = defineModel<RegistryField>({ required: true })

const kinds: FieldKind[] = ['string', 'text', 'markdown', 'boolean', 'integer', 'decimal', 'datetime', 'enum', 'relation']

function editProperty<Key extends 'key' | 'scope' | 'visibility' | 'target_type_id'>(key: Key) {
  return computed({
    get: () => field.value[key],
    set: value => { field.value = { ...field.value, [key]: value } },
  })
}

const fieldKey = editProperty('key')
const scope = editProperty('scope')
const visibility = editProperty('visibility')
const targetTypeID = editProperty('target_type_id')
const fieldKind = computed({
  get: () => field.value.kind,
  set: value => { field.value = { ...field.value, kind: value, config: undefined, target_type_id: undefined } },
})

function updateEnumCodes(event: Event) {
  const control = event.currentTarget
  if (!(control instanceof HTMLInputElement)) return
  const codes = control.value.split(',').map(code => code.trim()).filter(Boolean)
  field.value = { ...field.value, config: { codes } }
}

function editTranslation(part: DisplayPart, event: Event) {
  const control = event.currentTarget
  if (control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement) emit('translation', part, control.value)
}
</script>

<template>
  <details
    class="builder-field"
    :aria-labelledby="`registry-name-${rowId}`"
  >
    <summary>
      <span class="builder-summary">
        <strong :id="`registry-name-${rowId}`" class="builder-name" dir="auto">{{ text.summary }}</strong>
        <span class="builder-metadata">
          <span>{{ translate(`admin.registry_kind_${field.kind}`) }}</span>
          <span>{{ translate(`admin.registry_scope_${field.scope}`) }}</span>
          <span>{{ translate(`admin.registry_${field.visibility}`) }}</span>
          <span>{{ translate(field.required ? 'admin.registry_required' : 'admin.registry_optional') }}</span>
        </span>
      </span>
    </summary>
    <fieldset :disabled="busy">
      <p v-if="locked" class="builder-hint">{{ translate(existing ? 'admin.registry_field_locked' : 'admin.registry_template_locked') }}</p>
      <div v-else class="builder-field-controls">
        <div class="builder-control">
          <label :for="`registry-key-${rowId}`">{{ translate('admin.registry_field_key') }}</label>
          <input :id="`registry-key-${rowId}`" v-model="fieldKey" required pattern="[a-z][a-z0-9_]{0,63}" dir="ltr">
        </div>
        <div class="builder-control">
          <label :for="`registry-kind-${rowId}`">{{ translate('admin.registry_kind') }}</label>
          <select :id="`registry-kind-${rowId}`" v-model="fieldKind">
            <option v-for="kind in kinds" :key="kind" :value="kind">{{ translate(`admin.registry_kind_${kind}`) }}</option>
          </select>
        </div>
        <div class="builder-control">
          <label :for="`registry-scope-${rowId}`">{{ translate('admin.registry_scope') }}</label>
          <select :id="`registry-scope-${rowId}`" v-model="scope">
            <option value="localized">{{ translate('admin.registry_scope_localized') }}</option>
            <option value="shared">{{ translate('admin.registry_scope_shared') }}</option>
          </select>
        </div>
        <div class="builder-control">
          <label :for="`registry-visibility-${rowId}`">{{ translate('admin.registry_visibility') }}</label>
          <select :id="`registry-visibility-${rowId}`" v-model="visibility">
            <option value="private">{{ translate('admin.registry_private') }}</option>
            <option value="public">{{ translate('admin.registry_public') }}</option>
          </select>
        </div>
        <div v-if="field.kind === 'enum'" class="builder-control">
          <label :for="`registry-codes-${rowId}`">{{ translate('admin.registry_codes') }}</label>
          <input
            :id="`registry-codes-${rowId}`" required :value="field.config?.codes?.join(', ')"
            @input="updateEnumCodes"
          >
        </div>
        <div v-if="field.kind === 'relation'" class="builder-control">
          <label :for="`registry-target-${rowId}`">{{ translate('admin.registry_target') }}</label>
          <select :id="`registry-target-${rowId}`" v-model="targetTypeID" required>
            <option v-for="target in targets" :key="target.type_id" :value="target.type_id">{{ collectionLabel(target) }}</option>
          </select>
        </div>
      </div>
      <template v-if="language">
        <div class="builder-field-controls">
          <div class="builder-control">
            <label :for="`registry-label-${rowId}`">{{ translate('admin.registry_label') }}</label>
            <input
              :id="`registry-label-${rowId}`" :value="text.label" maxlength="4096"
              :placeholder="text.defaultLabel" :aria-describedby="`registry-default-${rowId}`"
              :lang="language.code" :dir="language.direction ?? 'auto'" @input="editTranslation('label', $event)"
            >
          </div>
          <div class="builder-control">
            <label :for="`registry-help-${rowId}`">{{ translate('admin.registry_help') }}</label>
            <textarea
              :id="`registry-help-${rowId}`" :value="text.help" maxlength="4096" rows="2"
              :aria-describedby="text.helpExample ? `registry-help-default-${rowId} registry-default-${rowId}` : `registry-default-${rowId}`"
              :lang="language.code" :dir="language.direction ?? 'auto'" @input="editTranslation('help', $event)"
            />
            <p
              v-if="text.helpExample" :id="`registry-help-default-${rowId}`"
              class="builder-hint" :lang="language.code" :dir="language.direction"
            >{{ text.helpExample }}</p>
          </div>
        </div>
        <p :id="`registry-default-${rowId}`" class="builder-hint">{{ translate('admin.registry_translation_help') }}</p>
      </template>
      <button v-if="!locked" type="button" class="admin-secondary" @click="emit('remove')">{{ translate('admin.registry_remove_field') }}</button>
    </fieldset>
  </details>
</template>
