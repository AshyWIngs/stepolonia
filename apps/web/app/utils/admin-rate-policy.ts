import { AdminContentError, administrationRequest } from './admin-request'

const endpoint = '/api/v1/admin/security/rate-policy'

const rateScopes = ['credentials', 'preauth', 'step_up', 'denial_audit'] as const
export type RateScope = typeof rateScopes[number]

const rateFields = ['window_seconds', 'subject_attempts', 'ip_attempts', 'installation_attempts', 'hash_jobs'] as const
export type RateField = typeof rateFields[number]

interface RateBound {
  minimum: number
  maximum: number
}

export type RateBounds = Record<RateField, RateBound>
export type RateDraft = Partial<Record<RateField, number | string>>

interface RateValues {
  window_seconds: number
  subject_attempts?: number
  ip_attempts: number
  installation_attempts: number
  hash_jobs?: number
}

export interface RatePolicy extends RateValues {
  scope: RateScope
  version: string
}

export function fieldsForRateScope(scope: RateScope) {
  return rateFields.filter(field => {
    if (field === 'subject_attempts') return scope !== 'preauth'
    if (field === 'hash_jobs') return scope === 'credentials'
    return true
  })
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function positiveInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function isBound(value: unknown): value is RateBound {
  return isRecord(value) && positiveInteger(value.minimum) && positiveInteger(value.maximum) && value.minimum <= value.maximum
}

function isBounds(value: unknown): value is RateBounds {
  return isRecord(value) && rateFields.every(field => isBound(value[field]))
}

function isScope(value: unknown): value is RateScope {
  return typeof value === 'string' && (rateScopes as readonly string[]).includes(value)
}

function withinBound(value: unknown, { minimum, maximum }: RateBound) {
  return positiveInteger(value) && value >= minimum && value <= maximum
}

export function validRateValues(values: unknown, scope: RateScope, bounds: RateBounds): values is RateValues {
  if (!isRecord(values)) return false
  const fields = fieldsForRateScope(scope)
  return rateFields.every(field => fields.includes(field) ? withinBound(values[field], bounds[field]) : values[field] === undefined)
}

function parsePolicy(value: unknown, bounds: RateBounds): RatePolicy {
  if (!isRecord(value) || !isScope(value.scope) || typeof value.version !== 'string' ||
      !/^[1-9]\d{0,18}$/u.test(value.version) || !validRateValues(value, value.scope, bounds)) {
    throw new AdminContentError(503)
  }
  return { ...value, scope: value.scope, version: value.version }
}

export async function readRatePolicies() {
  const result = await administrationRequest<unknown>(endpoint)
  if (!isRecord(result) || !Array.isArray(result.policies) || result.policies.length !== rateScopes.length || !isBounds(result.bounds)) {
    throw new AdminContentError(503)
  }
  const bounds = result.bounds
  const policies = result.policies.map((policy: unknown) => parsePolicy(policy, bounds))
  const ordered = rateScopes.map(scope => policies.find(policy => policy.scope === scope))
  if (!ordered.every((policy): policy is RatePolicy => policy !== undefined)) throw new AdminContentError(503)
  return { policies: ordered, bounds }
}

export async function saveRatePolicy(policy: RatePolicy, values: RateValues, bounds: RateBounds, csrf: string) {
  const result = await administrationRequest<unknown>(endpoint, csrf, 'POST', {
    ...values,
    scope: policy.scope,
    expected_version: policy.version,
  })
  const saved = parsePolicy(result, bounds)
  if (saved.scope !== policy.scope) throw new AdminContentError(503)
  return saved
}
