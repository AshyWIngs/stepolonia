<script setup lang="ts">
import { computed } from 'vue'
import { useAsyncData } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { usePublicUi } from '~/composables/usePublicUi'
import { contentPath, type ContentNamespace } from '~/utils/public-content'

const props = defineProps<{
  namespace: ContentNamespace
  language: string
  detail?: boolean
}>()

const { t } = usePublicUi()
const content = usePublicContent()
const alternate = computed(() => props.namespace === 'articles' ? 'events' : 'articles')
const { data: alternateAvailable } = await useAsyncData(
  () => `collection-navigation:${alternate.value}:${props.language}`,
  () => content.collectionAvailable(alternate.value, props.language),
)
const namespaces: ContentNamespace[] = ['articles', 'events']
const visible = computed(() => namespaces.filter(namespace => namespace === props.namespace || alternateAvailable.value))
</script>

<template>
  <nav :aria-label="t('navigation')" class="collection-nav">
    <NuxtLink
      v-for="entry in visible" :key="entry" :to="contentPath(entry, language)"
      :aria-current="entry === namespace ? (detail ? 'location' : 'page') : undefined"
    >{{ t(entry) }}</NuxtLink>
  </nav>
</template>

<style scoped>
.collection-nav {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 13rem));
  gap: .25rem .75rem;
  border-block-end: 1px solid currentColor;
}

.collection-nav a {
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  box-sizing: border-box;
  padding: .5rem .75rem;
  border-block-end: .2rem solid transparent;
  color: inherit;
  overflow-wrap: anywhere;
}

.collection-nav a[aria-current] {
  border-block-end-color: currentColor;
  font-weight: 700;
  text-decoration: none;
}
</style>
