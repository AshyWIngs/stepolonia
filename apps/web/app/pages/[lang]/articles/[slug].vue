<script setup lang="ts">
import { navigateTo, useAsyncData, useHead, useRoute, useRuntimeConfig } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { markdownDescription } from '~/utils/markdown'
import { articlesPath, canonicalUrl, publicError, routeSegment } from '~/utils/public-content'

const route = useRoute()
const language = routeSegment(route.params.lang).toLowerCase()
const slug = routeSegment(route.params.slug)
const content = usePublicContent()
const { data, error } = await useAsyncData(`article:${language}:${slug}`, () => content.article(language, slug), {
  getCachedData: (key, app) => app.isHydrating ? app.payload.data[key] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
if (data.value?.kind === 'redirect') {
  await navigateTo(data.value.path, { redirectCode: 301, external: import.meta.client })
}
const { site } = useRuntimeConfig().public
useHead(() => {
  if (data.value?.kind !== 'article') return {}
  const { article } = data.value
  const canonical = canonicalUrl(site.origin, articlesPath(article.language_code, article.slug))
  return {
    title: `${article.title} | ${site.name}`,
    htmlAttrs: { lang: article.language_code },
    meta: [{ name: 'description', content: markdownDescription(article.body_markdown) }],
    link: canonical ? [{ rel: 'canonical', href: canonical }] : [],
  }
})
</script>

<template>
  <section v-if="data?.kind === 'article'">
    <nav lang="ru" aria-label="Навигация по статьям">
      <NuxtLink :to="articlesPath(data.article.language_code)">Все статьи</NuxtLink>
    </nav>
    <article :lang="data.article.language_code" :dir="data.language.direction">
      <h1>{{ data.article.title }}</h1>
      <ArticleMarkdown :source="data.article.body_markdown" />
    </article>
  </section>
</template>
