<script setup lang="ts">
import { contentTitle } from '~/utils/content-fields'
import { computed } from 'vue'
import { useAsyncData, useHead, useRoute, useRuntimeConfig } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePublicUi } from '~/composables/usePublicUi'
import { contentPageSize, contentListPage, contentPath, contentSeoLinks, contentNamespace, publicError, routeSegment } from '~/utils/public-content'

defineOptions({ name: 'PublishedContentList' })

const route = useRoute()
const namespace = contentNamespace(route.params.namespace)
const language = routeSegment(route.params.lang).toLowerCase()
const page = contentListPage(route.query.page)
const content = usePublicContent()
const { t } = usePublicUi()
const { data, error } = await useAsyncData(`content:${namespace}:${language}:${page}`, () => content.list(namespace, language, (page - 1) * contentPageSize), {
  getCachedData: (key, app) => app.isHydrating ? app.payload.data[key] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
const { publication } = usePublicSite()
const site = computed(() => publication.value?.settings)
const origin = useRuntimeConfig().public.site.origin
const listPath = page > 1 ? `${contentPath(namespace, language)}?page=${page}` : contentPath(namespace, language)
const seoLinks = computed(() => {
  if (!data.value) return []
  const translations = page === 1 ? data.value.languages.map(item => ({ language: item.code })) : []
  return contentSeoLinks(origin, listPath, namespace, translations)
})
useHead(() => ({
  title: `${t(namespace)} | ${site.value?.title}`,
  link: seoLinks.value,
  meta: [{ name: 'description', content: site.value?.description }],
}))
</script>

<template>
  <section v-if="data">
    <ContentCollectionNav :namespace="namespace" :language="data.language.code" />
    <ContentLanguageNav :namespace="namespace" :languages="data.languages" :current="data.language.code" />
    <h1>{{ t(namespace) }}</h1>
    <ul v-if="data.items.length" class="article-list" :lang="data.language.code" :dir="data.language.direction">
      <li v-for="article in data.items" :key="article.item_id">
        <h2><NuxtLink :to="contentPath(namespace, article.language, article.slug)">{{ contentTitle(article) }}</NuxtLink></h2>
      </li>
    </ul>
    <p v-else>{{ t('empty') }}</p>
    <nav v-if="page > 1 || data.nextOffset !== null" :aria-label="t('pagination')" class="pagination">
      <NuxtLink v-if="page > 1" :to="{ path: contentPath(namespace, language), query: page > 2 ? { page: page - 1 } : {} }">{{ t('previous') }}</NuxtLink>
      <NuxtLink v-if="data.nextOffset !== null" :to="{ path: contentPath(namespace, language), query: { page: page + 1 } }">{{ t('next') }}</NuxtLink>
    </nav>
  </section>
</template>
