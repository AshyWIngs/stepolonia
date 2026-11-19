<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import type { RoleGrant, RoleGrantOption } from '~/utils/admin-access'
import { collectionName, coversGrant, fieldName, grantFromCover, groupRules, operationName, ruleDomain, ruleKey, ruleScope } from '~/utils/admin-role-rules'

const props = defineProps<{
  options: RoleGrantOption[]
  original: RoleGrant[]
  collectionKeys?: Record<string, string>
  busy: boolean
  translate: (key: string) => string
}>()
const grants = defineModel<RoleGrant[]>({ required: true })
const maxGrants = 64
const collectionFilter = ref('')
const firstRule = props.original[0] ?? props.options[0]
const domainFilter = ref(firstRule ? ruleDomain(firstRule.operation) : '')
const installationFilter = 'installation'
const expanded = ref('')
const chosenCover = shallowRef<RoleGrantOption>()

const available = computed(() => groupRules(props.options, ruleKey))
const stored = computed(() => new Map(props.original.map(grant => [ruleKey(grant), grant])))
const selected = computed(() => new Map(grants.value.map(grant => [ruleKey(grant), grant])))
const collections = computed(() => new Map([
  ...Object.entries(props.collectionKeys ?? {}),
  ...props.options.flatMap(option => option.type_id && option.type_key ? [[option.type_id, option.type_key] as const] : []),
]))
const ruleTemplates = computed(() => new Map([...props.options, ...props.original].map(grant => [ruleKey(grant), grant])))
const domains = computed(() => [...new Set([...ruleTemplates.value.values()].map(grant => ruleDomain(grant.operation)))])
const visibleRows = computed(() => {
  const rows = [...ruleTemplates.value].filter(([, row]) =>
    (!collectionFilter.value || (row.type_id ?? installationFilter) === collectionFilter.value) &&
    (!domainFilter.value || ruleDomain(row.operation) === domainFilter.value),
  )
  return groupRules(rows, ([, row]) => ruleDomain(row.operation))
})
const expandedGrant = computed(() => selected.value.get(expanded.value))
const eligibleCovers = computed(() => {
  const grant = expandedGrant.value
  return grant ? (available.value.get(expanded.value) ?? []).filter(cover => coversGrant(cover, grant)) : []
})
const coverIndex = computed({
  get: () => Math.max(0, eligibleCovers.value.findIndex(cover => cover === chosenCover.value)),
  set: index => { chosenCover.value = eligibleCovers.value[index] },
})
const activeCover = computed(() => eligibleCovers.value[coverIndex.value])

function supportsDetails(key: string, grant: RoleGrant) {
  return (available.value.get(key) ?? []).some(cover => coversGrant(cover, grant) &&
    Boolean(cover.can_delegate || cover.fields?.length || cover.delegate_operations?.length))
}

function showDetails(key: string) {
  expanded.value = expanded.value === key ? '' : key
  chosenCover.value = undefined
}

function toggleRule(key: string, checked: boolean) {
  if (!checked) {
    grants.value = grants.value.filter(grant => ruleKey(grant) !== key)
    if (expanded.value === key) expanded.value = ''
    return
  }
  if (selected.value.has(key) || grants.value.length >= maxGrants) return
  const original = stored.value.get(key)
  if (original) {
    grants.value = [...grants.value, original]
    return
  }
  const cover = available.value.get(key)?.[0]
  if (!cover) return
  grants.value = [...grants.value, grantFromCover(cover)]
  if (cover.fields?.length || cover.delegate_operations?.length) showDetails(key)
}

function updateExpanded(patch: Partial<RoleGrant>) {
  const existing = expandedGrant.value
  const cover = activeCover.value
  if (!existing || !cover) return
  const changed = { ...existing, ...patch }
  if (coversGrant(cover, changed)) {
    chosenCover.value = cover
    grants.value = grants.value.map(grant => ruleKey(grant) === expanded.value ? changed : grant)
  }
}

function toggleMember(values: string[] | undefined, member: string, enabled: boolean) {
  return enabled ? [...new Set([...(values ?? []), member])] : (values ?? []).filter(value => value !== member)
}

function updateCeiling(event: Event) {
  const scope = (event.target as HTMLSelectElement).value
  if (scope === 'assigned' || scope === 'any') updateExpanded({ object_ceiling: scope })
}

function coverLabel(cover: RoleGrantOption) {
  const cutoff = cover.object_ceiling && props.translate(`admin.role_rule_scope_${cover.object_ceiling}`)
  const fields = cover.fields?.map(field => fieldName(field.key, props.translate)).join(', ')
  const delegates = cover.delegate_operations?.map(operation => operationName(operation, props.translate)).join(', ')
  return [fields, delegates, cutoff,
    cover.can_delegate && props.translate('admin.roles_allow_delegation'),
    cover.may_delegate_content && props.translate('admin.roles_allow_delegable_content'),
  ].filter(Boolean).join(' · ')
}

function ruleCollection(grant: RoleGrant) {
  const type = grant.type_id && collections.value.get(grant.type_id)
  return type ? collectionName(type, props.translate) : grant.type_id
}

function grantDetails(grant: RoleGrant) {
  const fields = grant.field_ids?.map(id => {
    const key = props.options.find(cover => cover.type_id === grant.type_id &&
      cover.fields?.some(field => field.id === id))?.fields?.find(field => field.id === id)?.key
    return key ? fieldName(key, props.translate) : id
  })
  const delegates = grant.delegate_operations?.map(operation => operationName(operation, props.translate))
  return [...fields ?? [], ...delegates ?? [],
    grant.object_ceiling && props.translate(`admin.role_rule_scope_${grant.object_ceiling}`),
    grant.can_delegate && props.translate('admin.roles_allow_delegation'),
    grant.may_delegate_content && props.translate('admin.roles_allow_delegable_content'),
  ].filter(Boolean).join(' · ')
}
</script>

<template>
  <div class="role-rules">
    <div class="rule-filters">
      <div v-if="domains.length > 1" class="rule-filter">
        <label for="role-rule-domain">{{ translate('admin.role_rule_domain') }}</label>
        <select id="role-rule-domain" v-model="domainFilter" :disabled="busy">
          <option value="">{{ translate('all') }}</option>
          <option v-for="domain in domains" :key="domain" :value="domain">{{ translate(`admin.role_rule_group_${domain}`) }}</option>
        </select>
      </div>
      <div v-if="collections.size" class="rule-filter">
        <label for="role-rule-collection">{{ translate('admin.role_rule_filter') }}</label>
        <select id="role-rule-collection" v-model="collectionFilter" :disabled="busy">
          <option value="">{{ translate('admin.role_rule_all_scopes') }}</option>
          <option :value="installationFilter">{{ translate('admin.role_rule_scope_installation') }}</option>
          <option v-for="[id, key] in collections" :key="id" :value="id">{{ collectionName(key, translate) }}</option>
        </select>
      </div>
    </div>
    <p v-if="grants.length >= maxGrants">{{ translate('admin.role_rule_limit') }}</p>
    <p v-if="visibleRows.size === 0">{{ translate('empty') }}</p>
    <fieldset v-for="[domain, rows] in visibleRows" :key="domain" class="rule-group" :disabled="busy">
      <legend>{{ translate(`admin.role_rule_group_${domain}`) }}</legend>
      <ul class="rule-rows">
        <li v-for="[key, rule] in rows" :key="key" class="rule-row">
          <label class="rule-toggle">
            <input
              type="checkbox" :checked="selected.has(key)" :disabled="!selected.has(key) && grants.length >= maxGrants"
              @change="toggleRule(key, ($event.target as HTMLInputElement).checked)"
            >
            <span>
              {{ operationName(rule.operation, translate) }} · {{ ruleScope(rule, ruleCollection(rule), translate) }}
            </span>
          </label>
          <template v-if="selected.get(key)">
            <button
              v-if="supportsDetails(key, selected.get(key)!)" type="button" class="rule-configure"
              :aria-label="`${translate('admin.role_rule_configure')}: ${operationName(rule.operation, translate)} · ${ruleScope(rule, ruleCollection(rule), translate)}`"
              :aria-expanded="expanded === key" @click="showDetails(key)"
            >{{ translate('admin.role_rule_configure') }}</button>
            <small v-if="expanded !== key && grantDetails(selected.get(key)!)" class="rule-detail">
              {{ grantDetails(selected.get(key)!) }}
            </small>
          </template>
          <div v-if="expanded === key && expandedGrant && activeCover" class="rule-options">
            <label v-if="eligibleCovers.length > 1" class="rule-filter">
              {{ translate('admin.role_rule_coverage') }}
              <select v-model.number="coverIndex">
                <option v-for="(cover, index) in eligibleCovers" :key="index" :value="index">{{ coverLabel(cover) }}</option>
              </select>
            </label>
            <fieldset v-if="activeCover.fields?.length" aria-describedby="role-rule-field-help">
              <legend>{{ translate('admin.roles_fields') }}</legend>
              <p id="role-rule-field-help">{{ translate('admin.role_rule_field_help') }}</p>
              <div class="rule-field-grid">
                <label v-for="field in activeCover.fields" :key="field.id" class="rule-toggle">
                  <input
                    type="checkbox" :checked="expandedGrant.field_ids?.includes(field.id)"
                    @change="updateExpanded({ field_ids: toggleMember(expandedGrant.field_ids, field.id, ($event.target as HTMLInputElement).checked) })"
                  ><span>{{ fieldName(field.key, translate) }}</span>
                </label>
              </div>
            </fieldset>
            <fieldset v-if="activeCover.delegate_operations?.length">
              <legend>{{ translate('admin.roles_delegated_operations') }}</legend>
              <label v-for="operation in activeCover.delegate_operations" :key="operation" class="rule-toggle">
                <input
                  type="checkbox" :checked="expandedGrant.delegate_operations?.includes(operation)"
                  @change="updateExpanded({ delegate_operations: toggleMember(expandedGrant.delegate_operations, operation, ($event.target as HTMLInputElement).checked) })"
                >{{ operationName(operation, translate) }}
              </label>
            </fieldset>
            <label v-if="activeCover.object_ceiling === 'any'" class="rule-filter">
              {{ translate('admin.roles_delegation_scope') }}
              <select :value="expandedGrant.object_ceiling" @change="updateCeiling">
                <option value="assigned">{{ translate('admin.roles_scope_assigned') }}</option>
                <option value="any">{{ translate('admin.roles_scope_any') }}</option>
              </select>
            </label>
            <label v-if="activeCover.can_delegate" class="rule-toggle">
              <input type="checkbox" :checked="expandedGrant.can_delegate" @change="updateExpanded({ can_delegate: ($event.target as HTMLInputElement).checked })">
              {{ translate('admin.roles_allow_delegation') }}
            </label>
            <label v-if="activeCover.may_delegate_content" class="rule-toggle">
              <input
                type="checkbox" :checked="expandedGrant.may_delegate_content"
                @change="updateExpanded({ may_delegate_content: ($event.target as HTMLInputElement).checked })"
              >{{ translate('admin.roles_allow_delegable_content') }}
            </label>
          </div>
        </li>
      </ul>
    </fieldset>
  </div>
</template>

<style scoped>
.role-rules, .rule-filters, .rule-filter, .rule-options { display: grid; gap: .5rem; min-inline-size: 0; }
.rule-filters { grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr)); }
.rule-filter { max-inline-size: 28rem; }
.role-rules :is(button, select) { box-sizing: border-box; min-block-size: 44px; max-inline-size: 100%; border: 1px solid var(--admin-border); border-radius: .4rem; padding: .5rem .75rem; background: var(--admin-card); color: var(--admin-text); font: inherit; }
.role-rules fieldset { min-inline-size: 0; margin: 0; border: 1px solid var(--admin-border); border-radius: .4rem; }
.role-rules :focus-visible { outline: 3px solid var(--admin-focus); outline-offset: 2px; }
.role-rules .rule-group { margin-block: 0; }
.rule-rows { display: grid; gap: 0; list-style: none; margin: 0; padding: 0; }
.rule-row { display: grid; gap: .25rem .5rem; grid-template-columns: minmax(0, 1fr) auto; align-items: start; }
.rule-row + .rule-row { border-block-start: 1px solid var(--admin-border); }
.rule-toggle { display: flex; align-items: center; gap: .5rem; min-block-size: 44px; min-inline-size: 0; overflow-wrap: anywhere; }
.rule-toggle input { flex: none; inline-size: 1.125rem; min-block-size: 0; block-size: 1.125rem; margin: .25rem; accent-color: var(--admin-action); }
.rule-detail { color: var(--admin-muted); }
.rule-configure { margin-block: .25rem; }
.rule-detail, .rule-options { grid-column: 1 / -1; padding-inline-start: 1.875rem; }
.rule-options { padding-block-end: .5rem; }
.rule-options p { margin: 0; }
.rule-field-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr)); gap: .25rem .75rem; }
@media (max-width: 36rem) {
  .rule-row { grid-template-columns: minmax(0, 1fr); }
  .rule-configure { justify-self: start; margin-inline-start: 1.875rem; }
}
</style>
