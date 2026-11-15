<script setup lang="ts">
import { useAsyncData, useHead, useRoute, useRuntimeConfig } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { articlesPath, publicError, routeSegment } from '~/utils/public-content'

const route = useRoute()
const language = routeSegment(route.params.lang).toLowerCase()
const rawPage = route.query.page ?? '1'
if (typeof rawPage !== 'string' || !/^[1-9]\d{0,4}$/u.test(rawPage)) throw publicError(400)
const page = Number(rawPage)
const content = usePublicContent()
const { data, error } = await useAsyncData(`articles:${language}:${page}`, () => content.list(language, (page - 1) * 20), {
  getCachedData: (key, app) => app.isHydrating ? app.payload.data[key] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
const { site } = useRuntimeConfig().public
useHead(() => ({
  title: `Статьи | ${site.name}`,
  htmlAttrs: { lang: data.value?.language.code ?? language },
  meta: [{ name: 'description', content: site.description }],
}))
</script>

<template>
  <section v-if="data">
    <h1 lang="ru">Статьи <span :lang="data.language.code" :dir="data.language.direction">{{ data.language.native_name }}</span></h1>
    <ul v-if="data.articles.length" class="article-list" :lang="data.language.code" :dir="data.language.direction">
      <li v-for="article in data.articles" :key="article.article_id">
        <h2><NuxtLink :to="articlesPath(article.language_code, article.slug)">{{ article.title }}</NuxtLink></h2>
      </li>
    </ul>
    <p v-else lang="ru">На этом языке пока нет опубликованных статей.</p>
    <nav v-if="page > 1 || data.articles.length === 20" aria-label="Страницы списка" class="pagination" lang="ru">
      <NuxtLink v-if="page > 1" :to="{ path: articlesPath(language), query: page > 2 ? { page: page - 1 } : {} }">Предыдущая страница</NuxtLink>
      <NuxtLink v-if="data.articles.length === 20" :to="{ path: articlesPath(language), query: { page: page + 1 } }">Следующая страница</NuxtLink>
    </nav>
  </section>
</template>
