<script setup lang="ts">
import { ref } from 'vue'
import AdminDomainHeader from '~/components/AdminDomainHeader.vue'
import AdminIcon from '~/components/AdminIcon.vue'
import { svgNamespace } from '~/composables/useSvgImagePreview'

defineProps<{
  existing: boolean
  busy: boolean
  saveBlocked: boolean
  invalid: boolean
  conflict: boolean
  failure: string
  saved: boolean
  previewIcon: string
  previewUnavailable: boolean
  translate: (key: string) => string
}>()
const emit = defineEmits<{ edited: [], save: [], reload: [] }>()
const draft = defineModel<string>({ required: true })
const input = ref<HTMLTextAreaElement>()
const status = ref<HTMLOutputElement>()

function focusInput() { input.value?.focus() }
function focusStatus() { status.value?.focus() }

function clear() {
  draft.value = ''
  emit('edited')
  focusInput()
}

defineExpose({ focusInput, focusStatus })
</script>

<template>
  <component :is="existing ? 'form' : 'div'" class="admin-domain" @submit.prevent="emit('save')">
    <AdminDomainHeader
      v-if="existing" :title="translate('admin.registry_icon')" :save-label="translate('admin.registry_icon_save')"
      :disabled="saveBlocked"
    />
    <div class="builder-control builder-icon-editor">
      <label for="registry-collection-icon" :class="{ 'builder-icon-label--named': existing }">{{ translate('admin.registry_icon') }}</label>
      <div class="builder-icon-preview">
        <span v-if="!previewUnavailable" class="builder-icon-sample" data-testid="collection-icon-preview"><AdminIcon name="collection" :icon-svg="previewIcon" /></span>
        <span v-else>{{ translate('admin.svg_preview_unavailable') }}</span>
      </div>
      <p id="registry-icon-help" class="builder-hint">
        {{ translate('admin.registry_icon_help') }}
        <br><code>viewBox="0 0 24 24"</code> · 20 × 20 px
      </p>
      <textarea
        id="registry-collection-icon" ref="input" v-model="draft" :readonly="busy" maxlength="4096" rows="4"
        dir="ltr" spellcheck="false" :aria-invalid="invalid || undefined"
        :aria-describedby="invalid ? 'registry-icon-help registry-icon-error' : 'registry-icon-help'"
        @input="emit('edited')"
      />
      <p v-if="invalid" id="registry-icon-error" role="alert">
        {{ translate('admin.registry_icon_invalid') }}
        <code>xmlns="{{ svgNamespace }}"</code>
      </p>
      <button v-if="draft" type="button" class="admin-secondary" :disabled="busy" @click="clear">
        {{ translate('admin.registry_icon_default') }}
      </button>
      <p v-if="failure" role="alert">{{ translate(failure) }}</p>
      <output v-if="saved" ref="status" tabindex="-1">{{ translate('admin.registry_icon_saved') }}</output>
      <button v-if="conflict" type="button" class="admin-secondary" :disabled="busy" @click="emit('reload')">
        {{ translate('admin.settings_scoped_reload') }}
      </button>
    </div>
  </component>
</template>
