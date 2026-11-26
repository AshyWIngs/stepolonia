<script setup lang="ts">
import { useNuxtApp, useRoute, useRouter, useState } from '#imports'
import { computed, onMounted, useTemplateRef, watch } from 'vue'
import type { components } from '@stepolonia/sdk'
import { usePortalLanguagePreference } from '~/composables/usePortalLanguagePreference'
import { usePublicTheme } from '~/composables/usePublicTheme'
import { usePublicUi } from '~/composables/usePublicUi'
import { contentPath, type ContentNamespace } from '~/utils/public-content'

type Language = components['schemas']['LanguageDTO']

const props = defineProps<{
  languages: Language[]
  namespace: ContentNamespace
  current: Language
  currentPath?: string
  translations?: { language: string, slug: string }[]
}>()

const preference = usePortalLanguagePreference()
const route = useRoute()
const router = useRouter()
const nuxt = useNuxtApp()
const focusTarget = useState<{ path: string } | null>('public-language-focus', () => null)
const preferences = useTemplateRef('preferences')
const { publication, t } = usePublicUi()
const theme = usePublicTheme()
const site = publication?.settings
const publishedLanguages = new Set(publication?.languages.map(language => language.code))
const choices = computed(() => {
  const slugs = new Map(props.translations?.map(translation => [translation.language, translation.slug]))
  return props.languages
    .filter(language => publishedLanguages.has(language.code) && (!props.translations || slugs.has(language.code)))
    .map(language => ({
      ...language,
      href: language.code === props.current.code && props.currentPath
        ? props.currentPath : contentPath(props.namespace, language.code, slugs.get(language.code)),
    }))
})
const hasAlternateLanguage = computed(() => choices.value.some(language => language.code !== props.current.code))

function restoreFocusOnArrival(path: string) {
  focusTarget.value = { path }
  const request = focusTarget.value
  let arrived = false
  function clear() {
    if (focusTarget.value === request) focusTarget.value = null
    document.removeEventListener('focusin', clear)
    window.removeEventListener('popstate', clear)
    stopRoute()
    stopPage()
    stopError()
  }
  const stopRoute = watch(router.currentRoute, (to) => {
    if (arrived || to.fullPath !== path) clear()
    else arrived = true
  }, { flush: 'sync' })
  const stopPage = nuxt.hook('page:finish', () => {
    if (router.currentRoute.value.fullPath === path) clear()
  })
  const stopError = nuxt.hook('app:error', clear)
  document.addEventListener('focusin', clear, { once: true })
  window.addEventListener('popstate', clear, { once: true })
  return clear
}

async function followLanguage(code: string, href: string, keyboard: boolean) {
  preference.value = code
  const path = router.resolve(href).fullPath
  const clearFocus = keyboard && code !== props.current.code ? restoreFocusOnArrival(path) : undefined
  const failure = await router.push(href).catch(() => true)
  if (failure || router.currentRoute.value.fullPath !== path) clearFocus?.()
}

onMounted(() => {
  if (focusTarget.value?.path !== route.fullPath) return
  focusTarget.value = null
  // The theme control is the next header control when the destination has no alternate language.
  preferences.value?.querySelector('summary')?.focus()
})
</script>

<template>
  <header class="site-header">
    <div class="site-identity" :lang="site?.default_language">
      <p v-if="site?.tagline" class="eyebrow">{{ site.tagline }}</p>
      <NuxtLink to="/" class="site-name">{{ site?.title }}</NuxtLink>
    </div>
    <div v-if="publication" ref="preferences" class="site-preferences">
      <LanguageSwitcher
        :label="t('common.language')" :current="current" :choices="choices"
        :flags="publication.language_flags"
        :static-label-visible="!hasAlternateLanguage"
        @follow="followLanguage"
      />
      <ThemeSwitcher v-model="theme" :translate="t" />
    </div>
    <div v-if="site?.description || site?.notice" class="site-blurb" :lang="site.default_language">
      <p v-if="site.description" class="site-description">{{ site.description }}</p>
      <p v-if="site.notice" class="note">{{ site.notice }}</p>
    </div>
    <p v-if="!hasAlternateLanguage" class="site-interface-language site-content-language">
      {{ t('common.language') }} ·
      <span :lang="current.code" :dir="current.direction">{{ current.native_name }}</span>
    </p>
    <p v-if="publication && current.code !== publication.language.code" class="site-interface-language">
      {{ t('common.interface_texts') }}
      <span :lang="publication.language.code" :dir="publication.language.direction">{{ publication.language.native_name }}</span>
    </p>
  </header>
</template>
