<script setup lang="ts">
import { clearError, useHead, useRuntimeConfig } from '#imports'
import type { NuxtError } from '#app'

defineOptions({ name: 'PublicErrorPage' })

const props = defineProps<{ error: NuxtError }>()
const { site } = useRuntimeConfig().public
const messages: Partial<Record<number, string>> = {
  400: 'Некорректный адрес',
  404: 'Страница не найдена',
  503: 'Материалы временно недоступны',
}
const title = messages[props.error.status ?? 500] ?? 'Не удалось открыть страницу'
useHead({ title: `${title} | ${site.name}`, htmlAttrs: { lang: 'ru' } })
</script>

<template>
  <main lang="ru" :style="{ '--site-accent': site.accent }">
    <p>{{ site.name }}</p>
    <h1>{{ title }}</h1>
    <p v-if="error.status === 503">Попробуйте открыть страницу позднее.</p>
    <a href="/" @click.prevent="clearError({ redirect: '/' })">На главную</a>
  </main>
</template>
