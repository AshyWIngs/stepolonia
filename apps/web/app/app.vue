<script setup lang="ts">
import { useHeadSafe, useResponseHeader, useRoute } from '#imports'
import { computed, shallowRef, watchEffect } from 'vue'
import { usePortalAcceptLanguage } from '~/composables/usePortalLanguagePreference'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePublicTheme } from '~/composables/usePublicTheme'
import { isAdminPath } from '~/utils/admin-path'

const route = useRoute()
const admin = computed(() => isAdminPath(route.path))
const { publication, loadedPath, load } = usePublicSite()
if (!admin.value) await load(route.fullPath)
const displayed = shallowRef(publication.value)
watchEffect(() => {
  if (loadedPath.value === route.fullPath) displayed.value = publication.value
})
const site = computed(() => displayed.value?.settings)
usePortalAcceptLanguage()
const theme = usePublicTheme()
useHeadSafe(() => admin.value ? {} : ({
  title: site.value?.title,
  htmlAttrs: { lang: displayed.value?.language.code, dir: displayed.value?.language.direction, 'data-public-theme': theme.value },
  meta: [{ name: 'description', content: site.value?.description }],
}))
useResponseHeader('Cache-Control').value = 'no-store'
</script>

<template>
  <NuxtPage v-if="admin" />
  <main v-else class="public-site" :style="{ '--site-accent': site?.accent }">
    <NuxtPage :page-key="route => route.fullPath" />
  </main>
</template>

<style>
html {
  color-scheme: light dark;
  scrollbar-gutter: stable;
  --public-background: light-dark(#f7f5f0, #181d19);
  --public-text: light-dark(#242923, #edf2e9);
  --public-muted: light-dark(#62685f, #bbc7ba);
  --public-border: light-dark(#929a8e, #798a7b);
  --public-surface: light-dark(#eae8e1, #273129);
  background: var(--public-background);
  color: var(--public-text);
  font-family: system-ui, sans-serif;
}
html[data-public-theme="light"] { color-scheme: light; }
html[data-public-theme="dark"] { color-scheme: dark; }
body { margin: 0; }
.public-site {
  --public-link: color-mix(in srgb, var(--site-accent, #255bc9), var(--public-text) 70%);
  box-sizing: border-box;
  max-width: 52rem;
  margin: 6vh auto;
  padding: 2rem;
  border-top: .35rem solid var(--site-accent, var(--public-link));
}
.eyebrow { font-size: .8rem; letter-spacing: .12em; text-transform: uppercase; color: var(--public-muted); }
.site-name { color: inherit; font-family: Georgia, serif; font-size: 1.6rem; text-decoration: none; }
.site-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) max-content;
  align-items: start;
  gap: .65rem 1rem;
  margin-block-end: 2rem;
}
.site-identity { min-inline-size: 0; overflow-wrap: anywhere; }
.site-preferences { display: grid; grid-auto-flow: column; gap: .5rem; }
.site-header :is(.site-blurb, .site-interface-language) { grid-column: 1 / -1; }
.site-blurb p:first-child { margin-block-start: 0; }
.site-blurb p:last-child { margin-block-end: 0; }
.site-interface-language { margin: 0; color: var(--public-muted); font-size: .875rem; }
h1, h2, h3 { font-family: Georgia, serif; font-weight: 400; overflow-wrap: anywhere; }
h1 { font-size: clamp(2rem, 6vw, 3.5rem); line-height: 1.15; margin: 1.5rem 0; }
p, li { line-height: 1.7; }
.public-site a { color: var(--public-link); text-underline-offset: .2em; overflow-wrap: anywhere; }
.public-site :is(a, select):focus-visible { outline: .15rem solid var(--public-link); outline-offset: .25rem; }
.public-site .site-name { color: inherit; }
.note, .site-description { color: var(--public-muted); }
.article-list { list-style: none; padding: 0; }
.article-list li { padding: 1rem 0; border-bottom: 1px solid var(--public-border); }
.article-list h2 { margin: 0; font-size: 1.6rem; }
.pagination { display: flex; flex-wrap: wrap; gap: 1.5rem; margin-top: 2rem; }
.article-body { overflow-wrap: anywhere; }
.article-body pre { overflow-x: auto; padding: 1rem; background: var(--public-surface); direction: ltr; }
.article-body code { font-size: 0.9em; }
.article-body blockquote { margin-inline: 0; padding-inline-start: 1rem; border-inline-start: .2rem solid var(--public-link); }
.article-body table { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; }
.article-body th, .article-body td { padding: .5rem; border: 1px solid var(--public-border); }
@media (max-width: 40rem) { .public-site { margin: 0; padding: 1.25rem; } }
</style>
