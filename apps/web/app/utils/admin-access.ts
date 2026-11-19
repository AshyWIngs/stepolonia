import { administrationCommand, administrationRequest } from './admin-request'
import { ownValue } from './draft-edit-buffer'

const usersPath = '/api/v1/admin/users'
const rolesPath = '/api/v1/admin/roles'

export interface AccessUser {
  id: string
  login: string
  state: 'pending' | 'disabled' | 'active'
  version: number
}

export interface AccessRole {
  id: string
  name: string
  state: 'disabled' | 'active' | 'archived'
  version: number
  grants: RoleGrant[]
  collection_keys?: Record<string, string>
  display_names?: Record<string, string>
}

export interface RoleGrant {
  operation: string
  scope: 'installation' | 'content'
  can_delegate: boolean
  type_id?: string
  object_scope?: 'type' | 'assigned' | 'any'
  field_ids?: string[]
  target_scope?: 'installation' | 'type'
  object_ceiling?: 'assigned' | 'any'
  may_delegate_content?: boolean
  delegate_operations?: string[]
}

export interface RoleGrantOption extends Omit<RoleGrant, 'field_ids'> {
  type_key?: string
  fields?: { id: string, key: string }[]
}

export interface NamedSetOption {
  id: string
  state: string
}

export interface Membership extends NamedSetOption {
  name: string
  display_names?: Record<string, string>
  domain: 'content' | 'structure' | 'settings' | 'access' | 'mixed' | 'other'
}

export interface Assignee extends NamedSetOption {
  login: string
}

export interface MembershipSet {
  user_id: string
  version: number
  role_ids: string[]
  options: Membership[]
  max_roles: number
}

export interface AssigneeSet {
  item_id: string
  version: string
  assignee_ids: string[]
  options: Assignee[]
  max_assignees: number
}

interface OnboardingToken { token: string, expires_at: string, user: AccessUser }
interface Page<T> { items: T[], next_offset: number | null }

export type RoleAction = 'enable' | 'disable' | 'archive' | 'restore' | 'delete'

function recordPath(path: string, id: string) {
  return `${path}/${encodeURIComponent(id)}`
}

export function listAccessUsers(offset = 0) {
  return administrationRequest<Page<AccessUser>>(`${usersPath}?offset=${offset}`)
}

export function createAccessUser(login: string, csrf: string) {
  return administrationRequest<AccessUser>(usersPath, csrf, 'POST', { login })
}

export function issueInvitation(user: AccessUser, csrf: string) {
  return administrationRequest<OnboardingToken>(`${recordPath(usersPath, user.id)}/onboarding-token`, csrf, 'POST', { expected_version: user.version })
}

export function changeUserState(user: AccessUser, command: 'activate' | 'disable', csrf: string) {
  return administrationRequest<AccessUser>(`${recordPath(usersPath, user.id)}/${command}`, csrf, 'POST', { expected_version: user.version })
}

export function readMembership(user: AccessUser) {
  return administrationRequest<MembershipSet>(`${recordPath(usersPath, user.id)}/roles`)
}

export function replaceMembership(set: MembershipSet, role_ids: string[], csrf: string) {
  return administrationRequest<AccessUser>(`${recordPath(usersPath, set.user_id)}/roles`, csrf, 'PUT', { expected_version: set.version, role_ids })
}

export function listAccessRoles(offset = 0) {
  return administrationRequest<Page<AccessRole>>(`${rolesPath}?offset=${offset}`)
}

export function readAccessRole(role: AccessRole) {
  return administrationRequest<AccessRole>(recordPath(rolesPath, role.id))
}

type NamedRole = Pick<AccessRole, 'id' | 'name' | 'display_names'>

export function roleDisplayName(role: NamedRole, language: string, peers: readonly NamedRole[] = []) {
  const label = ownValue(role.display_names, language) || role.name
  return label !== role.name && peers.some(peer => peer.id !== role.id && (ownValue(peer.display_names, language) || peer.name) === label)
    ? `${label} · ${role.name}` : label
}

export function saveRoleDisplayName(role: AccessRole, language: string, label: string | null, csrf: string) {
  return administrationRequest<AccessRole>(`${recordPath(rolesPath, role.id)}/display-name`, csrf, 'POST', {
    expected_version: role.version, language_code: language, display_name: label,
  })
}

export function roleGrantOptions() {
  return administrationRequest<{ options: RoleGrantOption[] }>(`${rolesPath}/grant-options`)
}

export function createAccessRole(name: string, grants: RoleGrant[], csrf: string) {
  return administrationRequest<AccessRole>(rolesPath, csrf, 'POST', { name, grants })
}

type GrantKey = Pick<RoleGrant, 'operation' | 'type_id' | 'object_scope'>

function grantKey(grant: RoleGrant): GrantKey {
  return { operation: grant.operation, type_id: grant.type_id, object_scope: grant.object_scope }
}

function grantFingerprint(grant: RoleGrant) {
  const compare = (left: string, right: string) => left.localeCompare(right, 'en')
  return JSON.stringify([
    grant.scope, grant.can_delegate, grant.target_scope, grant.object_ceiling, grant.may_delegate_content ?? false,
    [...(grant.field_ids ?? [])].sort(compare), [...(grant.delegate_operations ?? [])].sort(compare),
  ])
}

export function accessRoleChanges(role: AccessRole, name: string, grants: RoleGrant[]) {
  const previous = new Map(role.grants.map(grant => [JSON.stringify(grantKey(grant)), grant]))
  const upsert = grants.filter((grant) => {
    const key = JSON.stringify(grantKey(grant))
    const before = previous.get(key)
    previous.delete(key)
    return !before || grantFingerprint(before) !== grantFingerprint(grant)
  })
  const remove = [...previous.values()].map(grantKey)
  return {
    name: name === role.name ? undefined : name,
    upsert: upsert.length ? upsert : undefined,
    remove: remove.length ? remove : undefined,
  }
}

export function changeAccessRole(role: AccessRole, change: ReturnType<typeof accessRoleChanges>, csrf: string) {
  return administrationRequest<AccessRole>(`${recordPath(rolesPath, role.id)}/changes`, csrf, 'POST', {
    ...change, expected_version: role.version,
  })
}

export function copyAccessRole(role: AccessRole, name: string, csrf: string) {
  return administrationRequest<AccessRole>(`${recordPath(rolesPath, role.id)}/copy`, csrf, 'POST', { name, expected_version: role.version })
}

export function updateRoleState(role: AccessRole, action: RoleAction, csrf: string) {
  const path = recordPath(rolesPath, role.id)
  const payload = { expected_version: role.version }
  if (action === 'delete') return administrationCommand(path, csrf, payload, 'DELETE')
  return administrationRequest<AccessRole>(`${path}/${action}`, csrf, 'POST', payload)
}

function assigneePath(type: string, itemID: string) {
  return `/api/v1/admin/content/${encodeURIComponent(type)}/${encodeURIComponent(itemID)}/assignees`
}

export function readAssignees(type: string, itemID: string) {
  return administrationRequest<AssigneeSet>(assigneePath(type, itemID))
}

export function replaceAssignees(type: string, set: AssigneeSet, assignee_ids: string[], csrf: string) {
  return administrationRequest<{ version: string }>(assigneePath(type, set.item_id), csrf, 'PUT', { expected_version: set.version, assignee_ids })
}
