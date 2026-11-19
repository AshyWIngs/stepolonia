<script setup lang="ts">
import { computed } from 'vue'
import { navigateTo, useAsyncData, useHead, useRoute, useRuntimeConfig } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { usePublicSite } from '~/composables/usePublicSite'
import { contentTitle, contentBody } from '~/utils/content-fields'
import { markdownDescription } from '~/utils/markdown'
import { contentPath, contentSeoLinks, contentNamespace, publicError, routeSegment } from '~/utils/public-content'

defineOptions({ name: 'PublishedContentDetail' })

const route = useRoute()
const namespace = contentNamespace(route.params.namespace)
const language = routeSegment(route.params.lang).toLowerCase()
const slug = routeSegment(route.params.slug)
const content = usePublicContent()
const { data, error } = await useAsyncData(`content:${namespace}:${language}:${slug}`, () => content.detail(namespace, language, slug), {
  getCachedData: (key, app) => app.isHydrating ? app.payload.data[key] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
if (data.value?.kind === 'redirect') {
  await navigateTo(data.value.path, { redirectCode: 301, external: import.meta.client })
}
const { publication } = usePublicSite()
const site = computed(() => publication.value?.settings)
const origin = useRuntimeConfig().public.site.origin
useHead(() => {
  if (data.value?.kind !== 'content') return {}
  const { item } = data.value
  return {
    title: `${contentTitle(item)} | ${site.value?.title}`,
    meta: [{ name: 'description', content: markdownDescription(contentBody(item)) }],
    link: contentSeoLinks(origin, contentPath(namespace, item.language, item.slug), namespace, item.translations),
  }
})
</script>

<template>
  <section v-if="data?.kind === 'content'">
    <PublicSiteHeader :namespace="namespace" :languages="data.languages" :current="data.language" :translations="data.item.translations" />
    <ContentCollectionNav :namespace="namespace" :language="data.language.code" detail />
    <article :lang="data.item.language" :dir="data.language.direction">
      <h1>{{ contentTitle(data.item) }}</h1>
      <ContentFields :item="data.item" />
    </article>
  </section>
</template>
