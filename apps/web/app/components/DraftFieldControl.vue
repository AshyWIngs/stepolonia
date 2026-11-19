<script setup lang="ts">
import { computed } from 'vue'
import type { EditorField, Scalar } from '~/utils/admin-content'

const props = defineProps<{
  field: EditorField
  label: string
  help?: string
  value?: Scalar
  invalid: boolean
  translate: (key: string) => string
}>()
const emit = defineEmits<{ change: [value: Scalar | undefined], 'clear-relation': [] }>()
const id = computed(() => `material-field-${props.field.field_id}`)
const describedBy = computed(() => [
  props.help && `${id.value}-help`,
  props.invalid && `${id.value}-error`,
].filter(Boolean).join(' ') || undefined)
const isTextArea = computed(() => ['text', 'markdown'].includes(props.field.kind))
const inputMode = computed(() => {
  if (props.field.kind === 'integer') return 'numeric'
  if (props.field.kind === 'decimal') return 'decimal'
  return 'text'
})

function change(event: Event) {
  const control = event.currentTarget
  if (!(control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement || control instanceof HTMLSelectElement)) return
  const { value } = control
  if (value === '') emit('change', undefined)
  else if (props.field.kind === 'boolean') emit('change', value === 'true')
  else emit('change', value)
}
</script>

<template>
  <div class="draft-field">
    <label :for="id">
      {{ label }}
      <small v-if="field.required">({{ translate('admin.content_required') }})</small>
    </label>
    <span v-if="help" :id="`${id}-help`">{{ help }}</span>
    <span v-if="invalid" :id="`${id}-error`" class="field-error">{{ translate('admin.content_required') }}</span>
    <select
      v-if="field.kind === 'boolean' || field.kind === 'enum'" :id="id" :value="String(value ?? '')"
      :disabled="!field.editable" :aria-invalid="invalid || undefined"
      :aria-describedby="describedBy" @change="change"
    >
      <option value="">{{ translate('admin.content_empty_value') }}</option>
      <template v-if="field.kind === 'boolean'">
        <option value="true">{{ translate('admin.content_yes') }}</option>
        <option value="false">{{ translate('admin.content_no') }}</option>
      </template>
      <option v-for="code in field.config.codes" :key="code" :value="code">{{ code }}</option>
    </select>
    <textarea
      v-else-if="isTextArea" :id="id" :value="String(value ?? '')" rows="4"
      :readonly="!field.editable" :aria-invalid="invalid || undefined"
      :aria-describedby="describedBy" @input="change"
    />
    <input
      v-else :id="id" type="text" :inputmode="inputMode" :value="String(value ?? '')"
      :readonly="!field.editable" :aria-invalid="invalid || undefined"
      :aria-describedby="describedBy"
      :autocomplete="field.kind === 'relation' ? 'off' : undefined"
      @input="change"
    >
    <button
      v-if="field.kind === 'relation' && field.editable" type="button" class="admin-secondary"
      @click="emit('clear-relation')"
    >{{ translate('admin.content_clear_relation') }}</button>
  </div>
</template>

<style scoped>
.draft-field {
  display: grid;
  gap: .35rem;
  min-width: 0;
  overflow-wrap: anywhere;
}

textarea, input, select {
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  min-height: 44px;
  padding: .6rem .75rem;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
}

[aria-invalid='true'] {
  border-width: 2px;
  border-color: var(--admin-error);
}

.field-error {
  color: var(--admin-error);
}
</style>
