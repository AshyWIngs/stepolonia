<script setup lang="ts">
import { computed } from 'vue'
import { contentFields, relationPath, type PublicContent } from '~/utils/content-fields'
const props = defineProps<{ item: PublicContent }>()
const fields = computed(() => contentFields(props.item))
</script>

<template>
  <div>
    <template v-for="{ key, field } in fields" :key="key">
      <ContentMarkdown v-if="field.kind === 'markdown'" :source="field.value" />
      <p v-else :data-field="key">{{ field.value }}</p>
    </template>
    <ul v-for="relation in item.relations" :key="relation.field_key">
      <li v-for="target in relation.targets" :key="target.item_id">
        <NuxtLink :to="relationPath(target.canonical_url)">{{ relation.field_key }}</NuxtLink>
      </li>
    </ul>
  </div>
</template>
