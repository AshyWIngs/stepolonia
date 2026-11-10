<script setup lang="ts">
const { site } = useRuntimeConfig().public
useHead({
  title: site.name,
  htmlAttrs: { lang: site.locale },
  meta: [{ name: 'description', content: site.description }],
})
</script>

<template>
  <main :style="{ '--site-accent': site.accent }">
    <p v-if="site.tagline" class="eyebrow">{{ site.tagline }}</p>
    <h1>{{ site.name }}</h1>
    <p v-if="site.description">{{ site.description }}</p>
    <p v-if="site.notice" class="note">{{ site.notice }}</p>
  </main>
</template>

<style>
html { color-scheme: light; background: #f7f5f0; color: #242923; font-family: system-ui, sans-serif; }
body { margin: 0; }
main { box-sizing: border-box; max-width: 52rem; margin: 12vh auto; padding: 2rem; border-top: 0.35rem solid var(--site-accent); }
.eyebrow { font-size: 0.8rem; letter-spacing: 0.12em; text-transform: uppercase; color: #6b5348; }
h1 { font-family: Georgia, serif; font-size: clamp(3rem, 10vw, 6rem); font-weight: 400; margin: 1.5rem 0; }
p { line-height: 1.6; }
.note { color: #62685f; }
</style>
