<script setup lang="ts">
import { computed } from 'vue'
import { clearError, useHead, useResponseHeader } from '#imports'
import type { NuxtError } from '#app'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePublicUi } from '~/composables/usePublicUi'

defineOptions({ name: 'PublicErrorPage' })
const props = defineProps<{ error: NuxtError }>()
const { publication } = usePublicSite()
const { t, uiLanguage } = usePublicUi()
useResponseHeader('Cache-Control').value = 'no-store'
// При сериализации Nuxt теряет getter status; statusCode сохраняет исходный HTTP-код.
const status = props.error.status ?? props.error.statusCode ?? 500
// Без опубликованного каталога ответ содержит только числовой статус, без запасного языка и ENV-текстов.
const title = computed(() => publication.value ? t([400, 404, 503].includes(status) ? `errors.${status}` : 'errors.other') : String(status))
useHead(() => ({ title: title.value, htmlAttrs: { lang: uiLanguage.value, dir: publication.value?.language.direction } }))
</script>

<template>
  <main :lang="uiLanguage" :style="{ '--site-accent': publication?.settings.accent }">
    <p v-if="publication" :lang="publication.settings.default_language">{{ publication.settings.title }}</p>
    <h1>{{ title }}</h1>
    <p v-if="publication && status === 503">{{ t('errors.retry') }}</p>
    <a v-if="publication" href="/" @click.prevent="clearError({ redirect: '/' })">{{ t('errors.home') }}</a>
  </main>
</template>
