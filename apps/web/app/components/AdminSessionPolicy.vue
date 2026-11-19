<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { AdminContentError, administrationRequest } from '~/utils/admin-request'
import { useAccessActions } from '~/utils/admin-access-feedback'

interface SessionPolicy {
  version: string
  idle_timeout_minutes: number
  absolute_timeout_hours: number
}

const props = defineProps<{ csrf: string, translate: (key: string) => string }>()
const emit = defineEmits<{
  'step-up': []
  'session-changed': []
  'editing-state': [state: { dirty: boolean, busy: boolean }]
}>()

const endpoint = '/api/v1/admin/security/session-policy'
const idleMin = 15
const idleMax = 60
const absoluteMin = 1
const absoluteMax = 12
const saved = ref<SessionPolicy>()
const idleMinutes = ref<number | string>('')
const absoluteHours = ref<number | string>('')
const { active, busy, error, feedback, run } = useAccessActions(() => emit('step-up'), () => emit('session-changed'))

const dirty = computed(() => saved.value !== undefined &&
  (idleMinutes.value !== saved.value.idle_timeout_minutes || absoluteHours.value !== saved.value.absolute_timeout_hours))
const conflicted = computed(() => error.value === 'admin.access_version_conflict')

watch([dirty, busy], ([dirty, busy]) => emit('editing-state', { dirty, busy }))

function integerInRange(value: unknown, min: number, max: number): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= min && value <= max
}

function acceptPolicy(value: unknown) {
  if (!value || typeof value !== 'object' || !('version' in value) ||
      typeof value.version !== 'string' || !/^[1-9]\d{0,18}$/u.test(value.version) ||
      !('idle_timeout_minutes' in value) || !integerInRange(value.idle_timeout_minutes, idleMin, idleMax) ||
      !('absolute_timeout_hours' in value) || !integerInRange(value.absolute_timeout_hours, absoluteMin, absoluteMax)) {
    throw new AdminContentError(503)
  }

  saved.value = {
    version: value.version,
    idle_timeout_minutes: value.idle_timeout_minutes,
    absolute_timeout_hours: value.absolute_timeout_hours,
  }
  idleMinutes.value = value.idle_timeout_minutes
  absoluteHours.value = value.absolute_timeout_hours
}

function load() {
  return run(async () => {
    const result = await administrationRequest<unknown>(endpoint)
    if (active.value) acceptPolicy(result)
  })
}

function save() {
  const current = saved.value
  const idle = idleMinutes.value
  const absolute = absoluteHours.value
  if (!current || !dirty.value || conflicted.value) return
  if (!integerInRange(idle, idleMin, idleMax) || !integerInRange(absolute, absoluteMin, absoluteMax) || idle > absolute * 60) {
    error.value = 'admin.access_input'
    feedback.value = ''
    return
  }
  return run(async () => {
    const result = await administrationRequest<unknown>(endpoint, props.csrf, 'PUT', {
      expected_version: current.version,
      idle_timeout_minutes: idle,
      absolute_timeout_hours: absolute,
    })
    if (!active.value) return
    acceptPolicy(result)
    feedback.value = 'admin.session_policy_saved'
  })
}

onMounted(load)
</script>

<template>
  <section class="session-policy" aria-labelledby="session-policy-heading" :aria-busy="busy">
    <h2 id="session-policy-heading">{{ translate('admin.session_policy_heading') }}</h2>
    <p id="session-policy-help">{{ translate('admin.session_policy_help') }}</p>
    <p v-if="error" role="alert">{{ translate(error) }}</p>
    <output v-if="feedback">{{ translate(feedback) }}</output>
    <output v-if="busy">{{ translate('admin.working') }}</output>
    <button v-if="conflicted" type="button" class="admin-secondary" :disabled="busy" @click="load">
      {{ translate('admin.access_reload') }}
    </button>
    <button v-else-if="!saved && error && error !== 'admin.access_denied'" type="button" class="admin-secondary" :disabled="busy" @click="load">
      {{ translate('admin.retry') }}
    </button>
    <form v-if="saved" aria-describedby="session-policy-help" @submit.prevent="save">
      <div class="session-policy-fields">
        <div>
          <label for="session-policy-idle-minutes">{{ translate('admin.session_policy_idle_minutes') }}</label>
          <input id="session-policy-idle-minutes" v-model.number="idleMinutes" type="number" :min="idleMin" :max="idleMax" step="1" required :readonly="busy">
        </div>
        <div>
          <label for="session-policy-absolute-hours">{{ translate('admin.session_policy_absolute_hours') }}</label>
          <input id="session-policy-absolute-hours" v-model.number="absoluteHours" type="number" :min="absoluteMin" :max="absoluteMax" step="1" required :readonly="busy">
        </div>
      </div>
      <button type="submit" :disabled="!dirty || busy || conflicted">{{ translate('admin.session_policy_save') }}</button>
    </form>
  </section>
</template>

<style scoped>
.session-policy {
  display: grid;
  gap: .5rem;
  min-inline-size: 0;
  margin-block-start: 1rem;
}

.session-policy h2, .session-policy p { margin: 0; }
.session-policy form { margin-block-start: .25rem; }
.session-policy button { justify-self: start; }

.session-policy-fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 14rem), 1fr));
  gap: .5rem 1rem;
  max-inline-size: 36rem;
}

.session-policy-fields div {
  display: grid;
  align-content: start;
  gap: .25rem;
  min-inline-size: 0;
}
</style>
