import { administrationCommand, administrationRequest } from './admin-request'

const root = '/api/v1/admin'

export interface AccountProfile {
  id: string
  login: string
  mfa_enabled: boolean
  recovery_codes_remaining: number
}

export interface AccountSession {
  id: string
  current: boolean
  created_at: string
  last_seen: string
  expires_at: string
}

export interface AuditEntry {
  id: string
  occurred_at: string
  operation: string
  result: string
  reason?: string
  actor_id?: string
  target_id?: string
  request_id?: string
}

export interface FactorPreparation {
  secret: string
  replacement_token: string
}

interface RecoveryCodes { recovery_codes: string[] }

export const accountPageLimit = 20
const maxPageOffset = 10_000

export function hasLaterAccountPage(offset: number, count: number) {
  return offset + accountPageLimit <= maxPageOffset && count === accountPageLimit
}

export function readProfile() {
  return administrationRequest<AccountProfile>(`${root}/me`)
}

export function listSessions(offset: number, userID?: string) {
  const path = userID ? `${root}/users/${encodeURIComponent(userID)}/sessions` : `${root}/sessions`
  return administrationRequest<{ items: AccountSession[] }>(`${path}?limit=${accountPageLimit}&offset=${offset}`)
}

export function listAudit(offset: number) {
  return administrationRequest<{ items: AuditEntry[] }>(`${root}/audit?limit=${accountPageLimit}&offset=${offset}`)
}

export function revokeSession(id: string, csrf: string) {
  return administrationCommand(`${root}/sessions/${encodeURIComponent(id)}/revoke`, csrf, {})
}

export function revokeAllSessions(csrf: string) {
  return administrationCommand(`${root}/sessions/revoke-all`, csrf, {})
}

export function changePassword(password: string, csrf: string) {
  return administrationCommand(`${root}/me/password`, csrf, { password })
}

export function prepareFactor(csrf: string) {
  return administrationRequest<FactorPreparation>(`${root}/me/mfa/replace`, csrf, 'POST', {})
}

export function completeFactor(replacement_token: string, code: string, csrf: string) {
  return administrationRequest<RecoveryCodes>(`${root}/me/mfa/replace`, csrf, 'POST', { replacement_token, code })
}

export function regenerateCodes(csrf: string) {
  return administrationRequest<RecoveryCodes>(`${root}/me/recovery-codes`, csrf, 'POST', {})
}
