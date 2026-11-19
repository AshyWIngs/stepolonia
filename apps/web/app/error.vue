<script setup lang="ts">
import { clearError, useHead, useResponseHeader } from '#imports'
import type { NuxtError } from '#app'
import { usePublicTheme } from '~/composables/usePublicTheme'
import { usePublicUi } from '~/composables/usePublicUi'

defineOptions({ name: 'PublicErrorPage' })
const props = defineProps<{ error: NuxtError }>()
const { publication, t } = usePublicUi()
const theme = usePublicTheme()
useResponseHeader('Cache-Control').value = 'no-store'
// Nuxt serialization drops the status getter. NuxtErrorJSON retains the HTTP status code.
const serializedError: Partial<ReturnType<NuxtError['toJSON']>> = props.error
const status = props.error.status ?? serializedError.statusCode ?? 500
const errorKey = [400, 404, 503].includes(status) ? `errors.${status}` : 'errors.other'
// Without a published catalog, the response renders only the numeric status.
const title = publication ? t(errorKey) : String(status)
useHead(() => ({
  title,
  htmlAttrs: { lang: publication?.language.code, dir: publication?.language.direction, 'data-public-theme': theme.value },
}))
</script>

<template>
  <main class="public-site" :lang="publication?.language.code" :style="{ '--site-accent': publication?.settings.accent }">
    <p v-if="publication" :lang="publication.settings.default_language">{{ publication.settings.title }}</p>
    <h1>{{ title }}</h1>
    <p v-if="publication && status === 503">{{ t('errors.retry') }}</p>
    <a v-if="publication" href="/" @click.prevent="clearError({ redirect: '/' })">{{ t('errors.home') }}</a>
  </main>
</template>
