<script setup lang="ts">
import { computed } from 'vue'
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
const { publication, t } = usePublicUi()
const theme = usePublicTheme()
const site = publication?.settings
const choices = computed(() => {
  const slugs = new Map(props.translations?.map(translation => [translation.language, translation.slug]))
  return props.languages
    .filter(language => !props.translations || slugs.has(language.code))
    .map(language => ({
      ...language,
      href: language.code === props.current.code && props.currentPath
        ? props.currentPath : contentPath(props.namespace, language.code, slugs.get(language.code)),
    }))
})
const hasAlternateLanguage = computed(() => choices.value.some(language => language.code !== props.current.code))
</script>

<template>
  <header class="site-header">
    <div class="site-identity" :lang="site?.default_language">
      <p v-if="site?.tagline" class="eyebrow">{{ site.tagline }}</p>
      <NuxtLink to="/" class="site-name">{{ site?.title }}</NuxtLink>
    </div>
    <div v-if="publication" class="site-preferences">
      <LanguageSwitcher
        :label="t('admin.language')" :current="current" :choices="choices"
        :static-label-visible="!hasAlternateLanguage"
        @select="preference = $event"
      />
      <ThemeSwitcher v-model="theme" :translate="t" />
    </div>
    <div v-if="site?.description || site?.notice" class="site-blurb" :lang="site.default_language">
      <p v-if="site.description" class="site-description">{{ site.description }}</p>
      <p v-if="site.notice" class="note">{{ site.notice }}</p>
    </div>
    <p v-if="!hasAlternateLanguage" class="site-interface-language site-content-language">
      {{ t('admin.language') }} ·
      <span :lang="current.code" :dir="current.direction">{{ current.native_name }}</span>
    </p>
    <p v-if="publication && current.code !== publication.language.code" class="site-interface-language">
      {{ t('admin.interface_texts') }}
      <span :lang="publication.language.code" :dir="publication.language.direction">{{ publication.language.native_name }}</span>
    </p>
  </header>
</template>
