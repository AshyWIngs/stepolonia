import type { RoleGrant, RoleGrantOption } from './admin-access'

export type RuleDomain = 'content' | 'structure' | 'accounts' | 'roles' | 'portal' | 'security' | 'other'

export function ruleDomain(operation: string): RuleDomain {
  if (operation.startsWith('content.items.')) return 'content'
  if (operation.startsWith('content_types.')) return 'structure'
  if (operation.startsWith('users.')) return 'accounts'
  if (operation.startsWith('roles.') || operation === 'content.permissions.delegate') return 'roles'
  if (operation.startsWith('settings.')) return 'portal'
  if (/^(sessions|security_policy|audit)\./u.test(operation)) return 'security'
  return 'other'
}

export function ruleKey(grant: RoleGrant) {
  return [grant.operation, grant.scope, grant.type_id, grant.object_scope, grant.target_scope].join('/')
}

type Translate = (key: string) => string
export const permissionMessagePrefix = 'admin.permission.'

function catalogName(key: string, fallback: string, translate: Translate) {
  const name = translate(key)
  return name === key ? fallback : name
}

export function operationName(operation: string, translate: Translate) {
  return catalogName(`${permissionMessagePrefix}${operation}`, operation, translate)
}

export function collectionName(name: string, translate: Translate) {
  return catalogName(`admin.registry_${name}`, name, translate)
}

export function fieldName(name: string, translate: Translate) {
  return catalogName(`admin.content_field_${name}`, name, translate)
}

export function ruleScope(grant: RoleGrant, collection: string | undefined, translate: Translate) {
  const scope = grant.object_scope ?? grant.target_scope ?? 'installation'
  const target = translate(`admin.role_rule_scope_${scope}`)
  return collection ? `${collection} · ${target}` : target
}

export function groupRules<T>(rows: T[], domainFor: (row: T) => string) {
  const groups = new Map<string, T[]>()
  for (const row of rows) {
    const domain = domainFor(row)
    const matches = groups.get(domain)
    if (matches) matches.push(row)
    else groups.set(domain, [row])
  }
  return groups
}

export function grantFromCover(cover: RoleGrantOption): RoleGrant {
  const grant: RoleGrant = { operation: cover.operation, scope: cover.scope, can_delegate: false }
  if (cover.type_id) grant.type_id = cover.type_id
  if (cover.object_scope) grant.object_scope = cover.object_scope
  if (cover.target_scope) {
    grant.target_scope = cover.target_scope
    grant.object_ceiling = 'assigned'
    grant.delegate_operations = []
    grant.may_delegate_content = false
  }
  return grant
}

export function coversGrant(cover: RoleGrantOption, grant: RoleGrant) {
  return (!grant.can_delegate || cover.can_delegate) &&
    (!grant.may_delegate_content || cover.may_delegate_content) &&
    (grant.object_ceiling !== 'any' || cover.object_ceiling === 'any') &&
    (grant.field_ids ?? []).every(id => cover.fields?.some(field => field.id === id)) &&
    (grant.delegate_operations ?? []).every(operation => cover.delegate_operations?.includes(operation))
}
