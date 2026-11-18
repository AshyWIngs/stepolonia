<script setup lang="ts">
import { useHeadSafe, useRequestHeader, useResponseHeader, useRoute, useState } from '#imports'
import { computed } from 'vue'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePublicUi } from '~/composables/usePublicUi'
import { isAdminPath } from '~/utils/admin-path'

const route = useRoute()
const admin = computed(() => isAdminPath(route.path))
const { publication, load } = usePublicSite()
if (!admin.value) await load(route.fullPath)
const site = computed(() => publication.value?.settings)
// Preserve the request header on direct content visits for later client navigation to the homepage.
useState('content-accept-language', () => useRequestHeader('accept-language') ?? '')
const { uiLanguage } = usePublicUi()
useHeadSafe(() => admin.value ? {} : ({
  title: site.value?.title,
  htmlAttrs: { lang: uiLanguage.value, dir: publication.value?.language.direction },
  meta: [{ name: 'description', content: site.value?.description }],
}))
useResponseHeader('Cache-Control').value = 'no-store'
</script>

<template>
  <NuxtPage v-if="admin" />
  <main v-else class="public-site" :style="{ '--site-accent': site?.accent }">
    <header :lang="site?.default_language" class="site-header">
      <p v-if="site?.tagline" class="eyebrow">{{ site?.tagline }}</p>
      <NuxtLink to="/" class="site-name">{{ site?.title }}</NuxtLink>
      <p v-if="site?.description" class="site-description">{{ site?.description }}</p>
      <p v-if="site?.notice" class="note">{{ site?.notice }}</p>
    </header>
    <NuxtPage :page-key="route => route.fullPath" />
  </main>
</template>

<style>
html { color-scheme: light; background: #f7f5f0; color: #242923; font-family: system-ui, sans-serif; }
body { margin: 0; }
.public-site { box-sizing: border-box; max-width: 52rem; margin: 6vh auto; padding: 2rem; border-top: 0.35rem solid var(--site-accent); }
.eyebrow { font-size: 0.8rem; letter-spacing: 0.12em; text-transform: uppercase; color: #6b5348; }
.site-name { color: inherit; font-family: Georgia, serif; font-size: 1.6rem; text-decoration: none; }
.site-header { margin-bottom: 2.5rem; }
h1, h2, h3 { font-family: Georgia, serif; font-weight: 400; overflow-wrap: anywhere; }
h1 { font-size: clamp(2rem, 6vw, 3.5rem); line-height: 1.15; margin: 1.5rem 0; }
p, li { line-height: 1.7; }
a { color: var(--site-accent); text-underline-offset: 0.2em; overflow-wrap: anywhere; }
a:focus-visible { outline: 0.15rem solid var(--site-accent); outline-offset: 0.25rem; }
.note, .site-description { color: #62685f; }
.article-list { list-style: none; padding: 0; }
.article-list li { padding: 1rem 0; border-bottom: 1px solid #d5d7cf; }
.article-list h2 { margin: 0; font-size: 1.6rem; }
.pagination { display: flex; flex-wrap: wrap; gap: 1.5rem; margin-top: 2rem; }
.language-nav { display: flex; flex-wrap: wrap; gap: 1rem; margin-block: 1.5rem; }
.language-nav [aria-current="page"] { font-weight: 700; }
.article-body { overflow-wrap: anywhere; }
.article-body pre { overflow-x: auto; padding: 1rem; background: #eae8e1; direction: ltr; }
.article-body code { font-size: 0.9em; }
.article-body blockquote { margin-inline: 0; padding-inline-start: 1rem; border-inline-start: 0.2rem solid var(--site-accent); }
.article-body table { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; }
.article-body th, .article-body td { padding: 0.5rem; border: 1px solid #d5d7cf; }
@media (max-width: 40rem) { .public-site { margin: 0; padding: 1.25rem; } }
</style>
