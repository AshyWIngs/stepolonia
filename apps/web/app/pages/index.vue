<script setup lang="ts">
import { navigateTo, useAsyncData, useRequestHeader, useRuntimeConfig, useState } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { useContentLanguageCookie } from '~/composables/useContentLanguageCookie'
import { preferredContentLanguage } from '~/utils/language'
import { articlesPath, publicError } from '~/utils/public-content'

defineOptions({ name: 'PortalHomePage' })

const { site } = useRuntimeConfig().public
const content = usePublicContent()
const saved = useContentLanguageCookie()
// Только сериализованный SSR header: navigator.languages мог бы изменить выбор после гидрации.
const accepted = useState('content-accept-language', () => useRequestHeader('accept-language') ?? '')
const { data, error } = await useAsyncData('entry-languages', () => content.listLanguages(), {
  getCachedData: (key, app) => app.isHydrating ? app.payload.data[key] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
if (!data.value) throw publicError(503)
const language = preferredContentLanguage(data.value.map(item => item.code), site.locale, saved.value, accepted.value)
await navigateTo(articlesPath(language), { redirectCode: 302 })
</script>

<template>
  <section />
</template>
