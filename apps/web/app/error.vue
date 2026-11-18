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
// Nuxt serialization drops the status getter. NuxtErrorJSON retains the HTTP status code.
const serializedError: Partial<ReturnType<NuxtError['toJSON']>> = props.error
const status = props.error.status ?? serializedError.statusCode ?? 500
const errorKey = [400, 404, 503].includes(status) ? `errors.${status}` : 'errors.other'
// Without a published catalog, the response renders only the numeric status.
const title = computed(() => publication.value ? t(errorKey) : String(status))
useHead(() => ({ title: title.value, htmlAttrs: { lang: uiLanguage.value, dir: publication.value?.language.direction } }))
</script>

<template>
  <main class="public-site" :lang="uiLanguage" :style="{ '--site-accent': publication?.settings.accent }">
    <p v-if="publication" :lang="publication.settings.default_language">{{ publication.settings.title }}</p>
    <h1>{{ title }}</h1>
    <p v-if="publication && status === 503">{{ t('errors.retry') }}</p>
    <a v-if="publication" href="/" @click.prevent="clearError({ redirect: '/' })">{{ t('errors.home') }}</a>
  </main>
</template>
