<script setup lang="ts">
import { navigateTo, useAsyncData } from '#imports'
import { usePublicContent } from '~/composables/usePublicContent'
import { usePublicSite } from '~/composables/usePublicSite'
import { usePortalAcceptLanguage, usePortalLanguagePreference } from '~/composables/usePortalLanguagePreference'
import { preferredLanguage } from '~/utils/language'
import { contentPath, publicError } from '~/utils/public-content'

defineOptions({ name: 'PortalHomePage' })

const { publication } = usePublicSite()
const saved = usePortalLanguagePreference()
const accepted = usePortalAcceptLanguage()
if (!publication.value) throw publicError(503)
const content = usePublicContent()
const { data, error } = await useAsyncData('entry-languages', () => content.listLanguages(), {
  getCachedData: (_key, app) => app.isHydrating ? app.payload.data['entry-languages'] : undefined,
})
if (error.value) throw publicError(error.value.status ?? 503)
if (!data.value) throw publicError(503)
const language = preferredLanguage(data.value.map(item => item.code), publication.value.settings.default_language, saved.value, accepted.value)
await navigateTo(contentPath('articles', language), { redirectCode: 302 })
</script>

<template>
  <section />
</template>
