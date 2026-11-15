<script setup lang="ts">
import { computed } from 'vue'
import { useAsyncData, useHead, useRoute } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePublicUi } from '~/composables/usePublicUi'
import { articlesPath, publicError, routeSegment } from '~/utils/public-content'

defineOptions({ name: 'PublishedArticleListPage' })

const route = useRoute()
const language = routeSegment(route.params.lang).toLowerCase()
const rawPage = route.query.page ?? '1'
if (typeof rawPage !== 'string' || !/^[1-9]\d{0,4}$/u.test(rawPage)) throw publicError(400)
const page = Number(rawPage)
const content = usePublicContent()
const { t } = usePublicUi()
const { data, error } = await useAsyncData(`articles:${language}:${page}`, () => content.list(language, (page - 1) * 20), {
  getCachedData: (key, app) => app.isHydrating ? app.payload.data[key] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
const { publication } = usePublicSite()
const site = computed(() => publication.value?.settings)
useHead(() => ({
  title: `${t('articles')} | ${site.value?.title}`,
  meta: [{ name: 'description', content: site.value?.description }],
}))
</script>

<template>
  <section v-if="data">
    <ContentLanguageNav :languages="data.languages" :current="data.language.code" />
    <h1>{{ t('articles') }} <span :lang="data.language.code" :dir="data.language.direction">{{ data.language.native_name }}</span></h1>
    <ul v-if="data.articles.length" class="article-list" :lang="data.language.code" :dir="data.language.direction">
      <li v-for="article in data.articles" :key="article.article_id">
        <h2><NuxtLink :to="articlesPath(article.language_code, article.slug)">{{ article.title }}</NuxtLink></h2>
      </li>
    </ul>
    <p v-else>{{ t('empty') }}</p>
    <nav v-if="page > 1 || data.articles.length === 20" :aria-label="t('pagination')" class="pagination">
      <NuxtLink v-if="page > 1" :to="{ path: articlesPath(language), query: page > 2 ? { page: page - 1 } : {} }">{{ t('previous') }}</NuxtLink>
      <NuxtLink v-if="data.articles.length === 20" :to="{ path: articlesPath(language), query: { page: page + 1 } }">{{ t('next') }}</NuxtLink>
    </nav>
  </section>
</template>
