<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { readAssignees, replaceAssignees } from '~/utils/admin-access'
import type { AssigneeSet } from '~/utils/admin-access'
import { accessNotice } from '~/utils/admin-access-feedback'

const props = defineProps<{ csrf: string, typeKey: string, itemId: string, translate: (key: string) => string }>()
const emit = defineEmits<{ 'step-up': [], 'session-changed': [], saved: [], close: [] }>()
const set = ref<AssigneeSet>()
const chosen = ref<string[]>([])
const confirm = ref(false)
const busy = ref(false)
const failure = ref('')
const heading = ref<HTMLElement>()
const additions = computed(() => chosen.value.filter(id => !set.value?.assignee_ids.includes(id)).length)
const removals = computed(() => set.value?.assignee_ids.filter(id => !chosen.value.includes(id)).length ?? 0)
let active = true

async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  try { await action() }
  catch (error: unknown) {
    if (active) failure.value = accessNotice(error, () => { emit('step-up') }, () => { emit('session-changed') })
  } finally { busy.value = false }
}

function load() {
  return run(async () => {
    const current = await readAssignees(props.typeKey, props.itemId)
    if (!active) return
    set.value = current
    chosen.value = current.assignee_ids.slice()
    confirm.value = false
  })
}

function save() {
  const current = set.value
  if (!current || !confirm.value) return
  return run(async () => {
    await replaceAssignees(props.typeKey, current, chosen.value, props.csrf)
    if (active) emit('saved')
  })
}

onMounted(async () => {
  await nextTick()
  heading.value?.focus()
  await load()
})
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="access-panel" aria-labelledby="draft-assignee-heading" :aria-busy="busy">
    <h3 id="draft-assignee-heading" ref="heading" tabindex="-1">{{ translate('admin.assignees') }}</h3>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <p v-if="busy" role="status">{{ translate('admin.working') }}</p>
    <fieldset v-if="set" :disabled="busy || confirm">
      <legend>{{ translate('admin.assignees_choose') }}</legend>
      <label v-for="account in set.options" :key="account.id" class="access-check">
        <input v-model="chosen" type="checkbox" :value="account.id" :disabled="!chosen.includes(account.id) && (account.state !== 'active' || chosen.length >= set.max_assignees)">
        {{ account.login }} · {{ translate(`admin.access_state_${account.state}`) }}
      </label>
    </fieldset>
    <div v-if="confirm" class="access-confirm">
      <p>{{ translate('admin.assignees_confirm') }} +{{ additions }} / −{{ removals }}</p>
      <div class="access-actions">
        <button type="button" :disabled="busy" @click="save">{{ translate('admin.verify') }}</button>
        <button type="button" :disabled="busy" @click="confirm = false">{{ translate('admin.cancel') }}</button>
      </div>
    </div>
    <div class="access-actions">
      <button v-if="set && !confirm" type="button" :disabled="busy || additions + removals === 0" @click="confirm = true">{{ translate('admin.assignees_save') }}</button>
      <button v-if="failure" type="button" :disabled="busy" @click="load">{{ translate('admin.access_reload') }}</button>
      <button type="button" class="admin-secondary" :disabled="busy" @click="emit('close')">{{ translate('admin.close') }}</button>
    </div>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
