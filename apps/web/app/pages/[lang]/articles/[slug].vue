<script setup lang="ts">
import { computed } from 'vue'
import { navigateTo, useAsyncData, useHead, useRoute, useRuntimeConfig } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePublicUi } from '~/composables/usePublicUi'
import { markdownDescription } from '~/utils/markdown'
import { articlesPath, contentSeoLinks, publicError, routeSegment } from '~/utils/public-content'

defineOptions({ name: 'PublishedArticlePage' })

const route = useRoute()
const language = routeSegment(route.params.lang).toLowerCase()
const slug = routeSegment(route.params.slug)
const content = usePublicContent()
const { t } = usePublicUi()
const { data, error } = await useAsyncData(`article:${language}:${slug}`, () => content.article(language, slug), {
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
  if (data.value?.kind !== 'article') return {}
  const { article } = data.value
  return {
    title: `${article.title} | ${site.value?.title}`,
    meta: [{ name: 'description', content: markdownDescription(article.body_markdown) }],
    link: contentSeoLinks(origin, articlesPath(article.language_code, article.slug), article.published_translations),
  }
})
</script>

<template>
  <section v-if="data?.kind === 'article'">
    <ContentLanguageNav :languages="data.languages" :current="data.language.code" :translations="data.article.published_translations" />
    <nav :aria-label="t('navigation')">
      <NuxtLink :to="articlesPath(data.article.language_code)">{{ t('all') }}</NuxtLink>
    </nav>
    <article :lang="data.article.language_code" :dir="data.language.direction">
      <h1>{{ data.article.title }}</h1>
      <ArticleMarkdown :source="data.article.body_markdown" />
    </article>
  </section>
</template>
