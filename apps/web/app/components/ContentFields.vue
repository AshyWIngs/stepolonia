<script setup lang="ts">
import { computed } from 'vue'
import { usePublicUi } from '~/composables/usePublicUi'
import { contentFields, formatContentValue, readableContentKey, relationPath, type PublicContent } from '~/utils/content-fields'

const props = defineProps<{ item: PublicContent }>()
const { t } = usePublicUi()
const fields = computed(() => contentFields(props.item))
const labels = computed(() => new Map(Object.entries(props.item.field_labels ?? {})))
const narratives = computed(() => fields.value.filter(entry => entry.field.kind === 'markdown'))
const details = computed(() => fields.value.filter(entry => entry.field.kind !== 'markdown'))
const related = computed(() => props.item.relations.flatMap(relation => relation.targets.map(target => {
  const path = relationPath(target.canonical_url)
  return { key: `${relation.field_key}:${target.item_id}`, path,
    label: labels.value.get(relation.field_key) ?? readableContentKey(path.split('/').at(-1) ?? '') }
})))
</script>

<template>
  <div class="published-fields">
    <div v-for="{ key, field } in narratives" :key="key">
      <h2 v-if="key !== 'body'">{{ labels.get(key) ?? readableContentKey(key) }}</h2>
      <ContentMarkdown v-if="field.kind === 'markdown'" :source="field.value" />
    </div>
    <dl v-if="details.length" class="content-details">
      <div v-for="{ key, field } in details" :key="key">
        <dt>{{ labels.get(key) ?? readableContentKey(key) }}</dt>
        <dd :data-field="key">
          <time v-if="field.kind === 'datetime'" :datetime="field.value">{{ formatContentValue(field, item.language) }}</time>
          <template v-else-if="field.kind === 'boolean'">{{ t(field.value ? 'admin.content_yes' : 'admin.content_no') }}</template>
          <template v-else>{{ formatContentValue(field, item.language) }}</template>
        </dd>
      </div>
    </dl>
    <section v-if="related.length" class="related-content" aria-labelledby="related-content-heading">
      <h2 id="related-content-heading">{{ t('related_content') }}</h2>
      <ul>
        <li v-for="link in related" :key="link.key"><NuxtLink :to="link.path">{{ link.label }}</NuxtLink></li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.content-details {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(11rem, 100%), 1fr));
  gap: 1rem;
  padding-block: 1rem;
  border-block: 1px solid var(--public-border);
}

.content-details dt {
  font-weight: 600;
  color: var(--public-muted);
}

.content-details dd {
  margin-inline-start: 0;
  overflow-wrap: anywhere;
  white-space: pre-line;
}

.related-content ul {
  padding-inline-start: 1.25rem;
}
</style>
