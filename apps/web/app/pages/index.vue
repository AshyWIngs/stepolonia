<script setup lang="ts">
import { useRuntimeConfig } from '#imports'
import { articlesPath } from '~/utils/public-content'

defineOptions({ name: 'PortalHomePage' })

const { site } = useRuntimeConfig().public
</script>

<template>
  <section :lang="site.locale">
    <h1>{{ site.name }}</h1>
    <NuxtLink :to="articlesPath(site.locale)" lang="ru">Статьи</NuxtLink>
  </section>
</template>
