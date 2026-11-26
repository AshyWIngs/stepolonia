<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import type { components } from '@stepolonia/sdk'
import { usePublicApi } from '~/composables/usePublicApi'
import { readAccessRole, saveRoleDisplayName, type AccessRole } from '~/utils/admin-access'
import { useAccessActions } from '~/utils/admin-access-feedback'
import { ownValue } from '~/utils/draft-edit-buffer'

const props = defineProps<{ role: AccessRole, csrf: string, uiLanguage: string, translate: (key: string) => string }>()
const emit = defineEmits<{
  saved: [role: AccessRole]
  close: []
  'step-up': []
  'session-changed': []
  'editing-state': [state: { dirty: boolean, busy: boolean }]
}>()
const { client, request } = usePublicApi()
const { active, busy, error, feedback, run } = useAccessActions(() => emit('step-up'), () => emit('session-changed'))
const languages = ref<components['schemas']['LanguageDTO'][]>([])
const language = ref('')
const value = ref('')
const outcome = ref<HTMLElement>()
const saved = computed(() => ownValue(props.role.display_names, language.value) ?? '')
const dirty = computed(() => Boolean(language.value) && value.value.trim() !== saved.value)
watch([dirty, busy], ([changed, saving]) => emit('editing-state', { dirty: changed, busy: saving }))

function choose(code: string) {
  language.value = code
  value.value = ownValue(props.role.display_names, code) ?? ''
  error.value = ''
  feedback.value = ''
}

onMounted(() => {
  void run(async () => {
    const result = await request(() => client.listLanguages())
    if (!result.data) throw new Error('Role translation languages are unavailable')
    if (!active.value) return
    languages.value = result.data
    choose(result.data.find(item => item.code === props.uiLanguage)?.code ?? result.data[0]?.code ?? '')
  })
})

async function save() {
  if (!dirty.value || error.value === 'admin.access_version_conflict') return
  await run(async () => {
    const updated = await saveRoleDisplayName(props.role, language.value, value.value.trim() || null, props.csrf)
    if (active.value) {
      emit('saved', updated)
      value.value = ownValue(updated.display_names, language.value) ?? ''
      feedback.value = 'admin.access_saved'
    }
  })
  await nextTick()
  outcome.value?.focus()
}

function reloadRole() {
  return run(async () => {
    const role = await readAccessRole(props.role)
    if (active.value) emit('saved', role)
  })
}
</script>

<template>
  <form class="admin-domain role-names" aria-labelledby="role-names-title" :aria-busy="busy" @submit.prevent="save">
    <AdminDomainHeader
      heading-id="role-names-title" :title="translate('admin.role_names_title')"
      :save-label="translate('admin.role_names_save')" :disabled="busy || !dirty || error === 'admin.access_version_conflict'"
    >
      <template #actions>
        <button type="button" :disabled="busy" @click="emit('close')">{{ translate('admin.cancel') }}</button>
      </template>
    </AdminDomainHeader>
    <span ref="outcome" tabindex="-1">
      <span v-if="error" role="alert">{{ translate(error) }}</span>
      <output v-if="feedback">{{ translate(feedback) }}</output>
    </span>
    <button v-if="error === 'admin.access_version_conflict'" type="button" :disabled="busy" @click="reloadRole">
      {{ translate('admin.access_reload') }}
    </button>
    <div v-if="languages.length" class="role-name-fields">
      <div>
        <label for="role-name-language">{{ translate('common.language') }}</label>
        <select id="role-name-language" :value="language" :disabled="busy || dirty" @change="choose(($event.target as HTMLSelectElement).value)">
          <option v-for="item in languages" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">{{ item.native_name }}</option>
        </select>
      </div>
      <div>
        <label for="role-name-translation">{{ translate('admin.roles_name') }}</label>
        <input id="role-name-translation" v-model="value" maxlength="128" dir="auto" :lang="language" :placeholder="role.name" :disabled="busy" aria-describedby="role-name-help">
      </div>
    </div>
    <small id="role-name-help">{{ translate('admin.role_names_help') }}</small>
  </form>
</template>

<style scoped>
.role-names, .role-name-fields > div { display: grid; gap: .5rem; min-inline-size: 0; }
.role-name-fields { display: grid; gap: 1rem; grid-template-columns: repeat(auto-fit, minmax(min(100%, 16rem), 1fr)); }
.role-names [tabindex="-1"]:empty { display: none; }
</style>
