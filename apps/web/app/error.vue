<script setup lang="ts">
import { clearError, useHead, useI18n, useResponseHeader, useRoute, useRuntimeConfig } from '#imports'
import type { NuxtError } from '#app'
import { isUiLocale, uiFallback } from '~/utils/language'

defineOptions({ name: 'PublicErrorPage' })

const props = defineProps<{ error: NuxtError }>()
const { site } = useRuntimeConfig().public
useResponseHeader('Cache-Control').value = 'no-store'
const { t, locale, fallbackLocale } = useI18n()
const fallback = uiFallback(site)
if (fallback) fallbackLocale.value = fallback
const routeCode = useRoute().params.lang
const code = typeof routeCode === 'string' ? routeCode.toLowerCase() : ''
const language = fallback && isUiLocale(code) ? code : fallback
if (language) locale.value = language
const status = props.error.status ?? 500
// При ошибке UI fallback не подставляем скрытый язык: безопасный ответ содержит только код статуса.
const messageKey = [400, 404, 503].includes(status) ? `errors.${status}` : 'errors.other'
const title = language ? t(messageKey) : String(status)
useHead({ title: `${title} | ${site.name}`, htmlAttrs: { lang: language } })
</script>

<template>
  <main :lang="language" :style="{ '--site-accent': site.accent }">
    <p :lang="site.locale">{{ site.name }}</p>
    <h1>{{ title }}</h1>
    <p v-if="language && status === 503">{{ t('errors.retry') }}</p>
    <a v-if="language" href="/" @click.prevent="clearError({ redirect: '/' })">{{ t('errors.home') }}</a>
  </main>
</template>
