<script setup lang="ts">
import { useRoute } from '#imports'
import { contentNamespace } from '~/utils/public-content'
defineOptions({ name: 'PublishedCollectionPage' })
const route = useRoute()
contentNamespace(route.params.namespace)
</script>

<template>
  <ContentDetail v-if="route.params.slug" />
  <ContentList v-else />
</template>
