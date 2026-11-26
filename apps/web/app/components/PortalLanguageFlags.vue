<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useSvgImagePreview } from '~/composables/useSvgImagePreview'
import type { PortalLanguage } from '~/utils/admin-site-settings'

const props = defineProps<{
  languages: PortalLanguage[]
  defaultLanguage: string
  flags: Record<string, string>
  translate: (key: string) => string
  invalid?: boolean
}>()
const updates = defineModel<Record<string, string>>({ required: true })
const selected = defineModel<string>('language', { required: true })
const sourceInput = ref<HTMLTextAreaElement>()

watch(() => props.languages, (languages) => {
  if (languages.some(language => language.code === selected.value)) return
  selected.value = languages.find(language => language.code === props.defaultLanguage)?.code ?? languages[0]?.code ?? ''
}, { immediate: true })

const saved = computed(() => props.flags[selected.value] ?? '')
const source = computed({
  get: () => updates.value[selected.value] ?? saved.value,
  set(value: string) {
    updates.value = value === saved.value
      ? Object.fromEntries(Object.entries(updates.value).filter(([code]) => code !== selected.value))
      : { ...updates.value, [selected.value]: value }
  },
})
const pending = computed(() => Object.hasOwn(updates.value, selected.value))
const { svg: preview, unavailable: previewUnavailable } = useSvgImagePreview(source, saved)

async function focusInvalid() {
  if (!pending.value) selected.value = Object.keys(updates.value)[0] ?? selected.value
  await nextTick()
  sourceInput.value?.focus()
}

defineExpose({ focusInvalid })
</script>

<template>
  <div class="flag-settings">
    <div class="flag-fields">
      <label for="portal-flag-language">{{ translate('common.language') }}</label>
      <select id="portal-flag-language" v-model="selected">
        <option v-for="language in languages" :key="language.code" :value="language.code" :lang="language.code" :dir="language.direction">
          {{ language.native_name }}
        </option>
      </select>
    </div>
    <div v-if="selected" class="flag-fields">
      <p class="flag-preview" data-testid="language-flag-preview">
        <LanguageFlag v-if="!previewUnavailable" :svg="preview" />
        {{ translate(previewUnavailable ? 'admin.svg_preview_unavailable' : 'admin.registry_icon_preview') }}
      </p>
      <label for="portal-language-flag-svg">{{ translate('admin.language_flag_svg') }}</label>
      <p id="portal-flag-help">
        {{ translate('admin.language_flag_help') }}
        <br><code>viewBox="0 0 30 20"</code> · 24 × 16 px
      </p>
      <textarea
        id="portal-language-flag-svg" ref="sourceInput" v-model="source" dir="ltr" spellcheck="false"
        rows="4" maxlength="4096" :aria-describedby="invalid && pending ? 'portal-flag-help scoped-settings-error' : 'portal-flag-help'"
        :aria-invalid="invalid && pending || undefined"
      />
      <button v-if="source.trim()" type="button" class="flag-reset" @click="source = ''">
        {{ translate('admin.language_flag_reset') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.flag-settings,
.flag-fields {
  display: grid;
  align-items: start;
  gap: .5rem;
  min-inline-size: 0;
}

.flag-preview {
  display: flex;
  align-items: center;
  gap: .5rem;
  margin: 0;
}

.flag-fields p { margin: 0; }
.flag-reset { justify-self: start; }

@media (min-width: 56rem) {
  .flag-settings { grid-template-columns: minmax(10rem, 1fr) minmax(0, 3fr); }
}
</style>
