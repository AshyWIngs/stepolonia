import { onBeforeUnmount, ref } from 'vue'
import { AdminContentError } from './admin-request'

const explanations: Record<string, string> = {
  permission_denied: 'admin.access_denied',
  step_up_required: 'admin.step_up_required',
  version_conflict: 'admin.access_version_conflict',
  last_admin_required: 'admin.access_last_admin',
  invalid_transition: 'admin.access_transition',
  role_in_use: 'admin.access_role_in_use',
  not_found: 'admin.access_unavailable_target',
  validation_failed: 'admin.access_input',
  rate_limited: 'admin.limited',
  csrf_failed: 'admin.changed',
  authentication_required: 'admin.changed',
}

export function accessNotice(error: unknown, stepUp: () => void, sessionChanged: () => void) {
  if (!(error instanceof AdminContentError)) return 'admin.unavailable'
  if (error.code === 'step_up_required') stepUp()
  else if (error.code === 'csrf_failed' || error.status === 401) sessionChanged()
  return explanations[error.code ?? ''] ?? (error.status === 403 ? 'admin.access_denied' : 'admin.unavailable')
}

export function useAccessActions(stepUp: () => void, sessionChanged: () => void) {
  const active = ref(true)
  const busy = ref(false)
  const error = ref('')
  const feedback = ref('')

  async function run(action: () => Promise<void>) {
    if (busy.value) return
    busy.value = true
    error.value = ''
    feedback.value = ''
    try { await action() }
    catch (problem: unknown) {
      if (active.value) error.value = accessNotice(problem, stepUp, sessionChanged)
    } finally { busy.value = false }
  }

  onBeforeUnmount(() => { active.value = false })
  return { active, busy, error, feedback, run }
}
