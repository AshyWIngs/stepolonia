<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { accountPageLimit, hasLaterAccountPage, listAudit } from '~/utils/admin-account'
import type { AuditEntry } from '~/utils/admin-account'
import { AdminContentError } from '~/utils/admin-request'

const props = defineProps<{ translate: (key: string) => string, uiLanguage: string }>()
const emit = defineEmits<{ 'session-changed': [] }>()
const items = ref<AuditEntry[]>([])
const offset = ref(0)
const busy = ref(false)
const failure = ref('')
let active = true

async function load(next = 0) {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  try {
    const out = await listAudit(next)
    if (!active) return
    offset.value = next
    items.value = out.items
  } catch (error: unknown) {
    if (!active) return
    if (error instanceof AdminContentError && error.status === 401) emit('session-changed')
    else if (error instanceof AdminContentError && error.status === 403) {
      items.value = []
      failure.value = 'admin.access_denied'
    } else failure.value = 'admin.unavailable'
  } finally { busy.value = false }
}

function date(value: string) {
  return Intl.DateTimeFormat(props.uiLanguage, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

onMounted(() => { void load() })
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="access-panel" :aria-busy="busy">
    <h2>{{ translate('admin.audit') }}</h2>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <p v-else-if="busy" role="status">{{ translate('admin.working') }}</p>
    <p v-else-if="!items.length" role="status">{{ translate('admin.audit_empty') }}</p>
    <button v-if="failure === 'admin.unavailable'" type="button" @click="load(offset)">{{ translate('admin.retry') }}</button>
    <ol class="access-rows">
      <li v-for="item in items" :key="item.id">
        <details>
          <summary>
            <time :datetime="item.occurred_at">{{ date(item.occurred_at) }}</time>
            · <code>{{ item.operation }}</code> · {{ translate(`admin.audit_${item.result}`) }}
          </summary>
          <dl>
            <div v-if="item.actor_id"><dt>{{ translate('admin.audit_actor') }}</dt><dd>{{ item.actor_id }}</dd></div>
            <div v-if="item.target_id"><dt>{{ translate('admin.audit_target') }}</dt><dd>{{ item.target_id }}</dd></div>
            <div v-if="item.request_id"><dt>{{ translate('admin.audit_request') }}</dt><dd>{{ item.request_id }}</dd></div>
            <div v-if="item.reason"><dt>{{ translate('admin.audit_reason') }}</dt><dd>{{ item.reason }}</dd></div>
          </dl>
        </details>
      </li>
    </ol>
    <div v-if="!failure" class="access-actions">
      <button v-if="offset" type="button" :disabled="busy" @click="load(offset - accountPageLimit)">{{ translate('previous') }}</button>
      <button v-if="hasLaterAccountPage(offset, items.length)" type="button" :disabled="busy" @click="load(offset + accountPageLimit)">{{ translate('next') }}</button>
    </div>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';
</style>
