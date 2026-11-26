<script setup lang="ts">
import { useRoute } from '#imports'
import { providePublicUi } from '~/composables/usePublicUi'
import { contentNamespace } from '~/utils/public-content'
defineOptions({ name: 'PublishedCollectionPage' })
const route = useRoute()
contentNamespace(route.params.namespace)
providePublicUi()
</script>

<template>
  <ContentDetail v-if="route.params.slug" />
  <ContentList v-else />
</template>
