<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { accessNotice, useAccessActions } from '~/utils/admin-access-feedback'
import { fieldsForRateScope, readRatePolicies, saveRatePolicy, validRateValues } from '~/utils/admin-rate-policy'
import type { RateBounds, RateDraft, RateField, RatePolicy, RateScope } from '~/utils/admin-rate-policy'
import { AdminContentError } from '~/utils/admin-request'

interface PolicyEditor {
  saved: RatePolicy
  draft: RateDraft
  failure: string
  feedback: string
  conflicted: boolean
  retrySeconds?: number
  review: RateField[]
}

const props = defineProps<{ csrf: string, translate: (key: string) => string }>()
const emit = defineEmits<{
  'step-up': []
  'session-changed': []
  'editing-state': [state: { dirty: boolean, busy: boolean }]
}>()

const editors = ref<PolicyEditor[]>([])
const bounds = ref<RateBounds>()
const timers = new Map<RateScope, ReturnType<typeof setTimeout>>()
const stepUp = () => emit('step-up')
const sessionChanged = () => emit('session-changed')
const { active, busy, error: loadError, run } = useAccessActions(stepUp, sessionChanged)
const dirty = computed(() => editors.value.some(hasChanges))

watch([dirty, busy], ([dirty, busy]) => emit('editing-state', { dirty, busy }))

function hasChanges(editor: PolicyEditor) {
  return fieldsForRateScope(editor.saved.scope).some(field => editor.saved[field] !== editor.draft[field])
}

function draftFor(policy: RatePolicy): RateDraft {
  return Object.fromEntries(fieldsForRateScope(policy.scope).map(field => [field, policy[field]]))
}

function editorFor(saved: RatePolicy): PolicyEditor {
  return { saved, draft: draftFor(saved), failure: '', feedback: '', conflicted: false, review: [] }
}

function fieldLabel(scope: RateScope, field: RateField) {
  return props.translate(field === 'subject_attempts' && scope === 'denial_audit' ? 'admin.rate_action_attempts' : `admin.rate_${field}`)
}

function load() {
  return run(async () => {
    const result = await readRatePolicies()
    if (!active.value) return
    bounds.value = result.bounds
    editors.value = result.policies.map(editorFor)
  })
}

function rebase(editor: PolicyEditor, saved: RatePolicy) {
  const changed = fieldsForRateScope(saved.scope).filter(field => editor.saved[field] !== editor.draft[field])
  const draft = draftFor(saved)
  for (const field of changed) draft[field] = editor.draft[field]
  editor.review = changed.filter(field => saved[field] !== editor.saved[field] && saved[field] !== draft[field])
  editor.saved = saved
  editor.draft = draft
  editor.conflicted = false
}

async function reload(editor: PolicyEditor) {
  if (busy.value) return
  await run(async () => {
    editor.failure = ''
    editor.feedback = ''
    try {
      const result = await readRatePolicies()
      if (!active.value) return
      const saved = result.policies.find(policy => policy.scope === editor.saved.scope)
      if (!saved) throw new AdminContentError(503)
      bounds.value = result.bounds
      rebase(editor, saved)
    }
    catch (error: unknown) {
      if (active.value) showFailure(editor, error)
    }
  })
  if (active.value && !editor.failure) {
    await nextTick()
    document.getElementById(`rate-${editor.saved.scope}-window_seconds`)?.focus()
  }
}

function holdRetry(editor: PolicyEditor, seconds: number) {
  const scope = editor.saved.scope
  clearTimeout(timers.get(scope))
  editor.retrySeconds = Math.min(seconds, 86400)
  timers.set(scope, setTimeout(() => {
    timers.delete(scope)
    editor.retrySeconds = undefined
    if (editor.failure === 'admin.limited') editor.failure = ''
  }, editor.retrySeconds * 1000))
}

function showFailure(editor: PolicyEditor, error: unknown) {
  if (error instanceof AdminContentError) {
    if (error.code === 'version_conflict') editor.conflicted = true
    if (error.status === 429) {
      holdRetry(editor, error.retryAfter ?? 1)
      editor.failure = 'admin.limited'
      return
    }
  }
  editor.failure = accessNotice(error, stepUp, sessionChanged)
}

function failureMessage(editor: PolicyEditor) {
  if (editor.retrySeconds && editor.failure === 'admin.limited') {
    return props.translate('admin.rate_retry_seconds').replace('{seconds}', String(editor.retrySeconds))
  }
  return props.translate(editor.failure)
}

async function save(editor: PolicyEditor) {
  const currentBounds = bounds.value
  if (!currentBounds || busy.value || editor.conflicted || editor.retrySeconds || !hasChanges(editor)) return
  const { saved, draft } = editor
  if (!validRateValues(draft, saved.scope, currentBounds)) {
    editor.feedback = ''
    editor.failure = 'admin.access_input'
    return
  }
  await run(async () => {
    editor.failure = ''
    editor.feedback = ''
    try {
      const updated = await saveRatePolicy(saved, draft, currentBounds, props.csrf)
      if (!active.value) return
      Object.assign(editor, editorFor(updated), { feedback: 'admin.rate_policy_saved' })
    }
    catch (error: unknown) {
      if (active.value) showFailure(editor, error)
    }
  })
  await focusOutcome(editor)
}

async function focusOutcome(editor: PolicyEditor) {
  if (!active.value || editor.failure === 'admin.changed') return
  await nextTick()
  const form = document.querySelector(`[data-rate-scope="${editor.saved.scope}"]`)
  const reload = form?.querySelector<HTMLElement>('.rate-domain-reload:not(:disabled)')
  const message = form?.querySelector<HTMLElement>('.rate-domain-status, .rate-domain-error')
  const target = reload ?? message
  target?.focus()
}

onMounted(load)
onBeforeUnmount(() => timers.forEach(timer => clearTimeout(timer)))
</script>

<template>
  <section class="access-panel" aria-labelledby="rate-policy-heading" :aria-busy="busy">
    <h2 id="rate-policy-heading">{{ translate('admin.rate_limits') }}</h2>
    <p v-if="loadError" role="alert">{{ translate(loadError) }}</p>
    <output v-if="busy && !bounds">{{ translate('admin.working') }}</output>
    <button v-if="!bounds && loadError && loadError !== 'admin.access_denied'" type="button" :disabled="busy" @click="load">
      {{ translate('admin.retry') }}
    </button>
    <div v-if="bounds" class="rate-domains">
      <form
        v-for="editor in editors" :key="editor.saved.scope" class="admin-domain" :data-rate-scope="editor.saved.scope"
        :aria-labelledby="`rate-${editor.saved.scope}-heading`" @submit.prevent="save(editor)"
      >
        <AdminDomainHeader
          :title="translate(`admin.rate_${editor.saved.scope}`)" :heading-id="`rate-${editor.saved.scope}-heading`"
          :save-label="translate('admin.rate_policy_save')"
          :disabled="busy || !hasChanges(editor) || editor.conflicted || Boolean(editor.retrySeconds)"
        />
        <output v-if="editor.feedback" class="rate-domain-status" tabindex="-1">{{ translate(editor.feedback) }}</output>
        <p v-if="editor.failure" class="rate-domain-error" role="alert" tabindex="-1">{{ failureMessage(editor) }}</p>
        <button
          v-if="editor.conflicted" class="rate-domain-reload" type="button" :disabled="busy || Boolean(editor.retrySeconds)"
          @click="reload(editor)"
        >{{ translate('admin.rate_policy_reload') }}</button>
        <details v-if="editor.review.length && hasChanges(editor)" open>
          <summary>{{ translate('admin.settings_review_saved_values') }}</summary>
          <dl>
            <div v-for="field in editor.review" :key="field">
              <dt>{{ fieldLabel(editor.saved.scope, field) }}</dt>
              <dd>{{ editor.saved[field] }}</dd>
            </div>
          </dl>
        </details>
        <div class="rate-fields">
          <div v-for="field in fieldsForRateScope(editor.saved.scope)" :key="field" class="rate-field">
            <label :for="`rate-${editor.saved.scope}-${field}`">{{ fieldLabel(editor.saved.scope, field) }}</label>
            <input
              :id="`rate-${editor.saved.scope}-${field}`" v-model.number="editor.draft[field]" :name="field" type="number"
              :min="bounds[field].minimum" :max="bounds[field].maximum" step="1" required :readonly="busy"
              :aria-describedby="field === 'hash_jobs' ? 'rate-hash-help' : undefined"
            >
          </div>
        </div>
        <p v-if="editor.saved.scope === 'credentials'" id="rate-hash-help">{{ translate('admin.rate_hash_help') }}</p>
      </form>
    </div>
  </section>
</template>

<style scoped>
@import '../assets/admin-access.css';

.rate-domains,
.rate-fields {
  display: grid;
  gap: .75rem;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
}

.rate-domains {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, max(27rem, calc((100% - .75rem) / 2))), 1fr));
  align-items: start;
}

.rate-field {
  display: grid;
  align-content: start;
  gap: .25rem;
  min-inline-size: 0;
}

.rate-domain-reload { justify-self: start; }
</style>
