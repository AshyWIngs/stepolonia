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

const eventLabels = new Map([
  ['auth.login_completed', 'admin.sign_in'],
  ['auth.logout', 'admin.sign_out'],
  ['auth.password_changed', 'admin.account_change_password'],
  ['auth.session_revoked', 'admin.account_revoke_session'],
  ['auth.sessions_revoked', 'admin.account_revoke_all'],
  ['auth.step_up', 'admin.fresh_auth'],
  ['users.created', 'admin.users_create'],
  ['users.onboarding_issued', 'admin.users_issue_invite'],
  ['users.activated', 'admin.users_activate'],
  ['users.disabled', 'admin.permission.users.disable'],
  ['users.roles_replaced', 'admin.users_apply_roles'],
  ['roles.created', 'admin.roles_create'],
  ['roles.edited', 'admin.roles_edit'],
  ['roles.copied', 'admin.roles_copy'],
  ['roles.enabled', 'admin.permission.roles.enable'],
  ['roles.disabled', 'admin.permission.roles.disable'],
  ['roles.archived', 'admin.permission.roles.archive'],
  ['roles.restored', 'admin.permission.roles.restore'],
  ['roles.deleted', 'admin.permission.roles.delete'],
  ['content.assignees_replaced', 'admin.assignees_save'],
  ['content.draft_created', 'admin.content_create'],
  ['content.draft_saved', 'admin.save_draft'],
  ['content.draft_ready', 'admin.permission.content.items.ready'],
  ['content.draft_published', 'admin.content_publish'],
  ['content.draft_unpublished', 'admin.content_unpublish'],
  ['content.draft_schema_adopted', 'admin.content_adopt_schema'],
  ['content.collection_created', 'admin.registry_create'],
  ['content.collection_revised', 'admin.registry_save'],
  ['content.collection_enabled', 'admin.registry_enable'],
  ['content.collection_disabled', 'admin.registry_disable'],
  ['settings.published', 'admin.settings_save_changes'],
  ['security.session_policy_changed', 'admin.session_policy_save'],
  ['mfa.replaced', 'admin.account_replace_factor'],
  ['mfa.recovery_codes_regenerated', 'admin.account_regenerate_codes'],
])

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

function eventLabel(operation: string) {
  const key = eventLabels.get(operation)
  const label = key && props.translate(key)
  return label && label !== key ? label : operation
}

onMounted(() => { void load() })
onBeforeUnmount(() => { active = false })
</script>

<template>
  <section class="access-panel" :aria-busy="busy">
    <h2>{{ translate('admin.audit') }}</h2>
    <p v-if="failure" role="alert">{{ translate(failure) }}</p>
    <p v-else-if="busy"><output>{{ translate('admin.working') }}</output></p>
    <p v-else-if="!items.length"><output>{{ translate('admin.audit_empty') }}</output></p>
    <button v-if="failure === 'admin.unavailable'" type="button" @click="load(offset)">{{ translate('admin.retry') }}</button>
    <ol class="access-rows">
      <li v-for="item in items" :key="item.id">
        <details>
          <summary>
            <time :datetime="item.occurred_at">{{ date(item.occurred_at) }}</time>
            · <span>{{ eventLabel(item.operation) }}</span> · {{ translate(`admin.audit_${item.result}`) }}
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
