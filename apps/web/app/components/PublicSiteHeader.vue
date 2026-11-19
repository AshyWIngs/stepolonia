<script setup lang="ts">
import { computed } from 'vue'
import type { components } from '@stepolonia/sdk'
import { usePortalLanguagePreference } from '~/composables/usePortalLanguagePreference'
import { publicThemes, usePublicTheme } from '~/composables/usePublicTheme'
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
</script>

<template>
  <header class="site-header">
    <div class="site-identity" :lang="site?.default_language">
      <p v-if="site?.tagline" class="eyebrow">{{ site.tagline }}</p>
      <NuxtLink to="/" class="site-name">{{ site?.title }}</NuxtLink>
      <p v-if="site?.description" class="site-description">{{ site.description }}</p>
      <p v-if="site?.notice" class="note">{{ site.notice }}</p>
    </div>
    <div v-if="publication" class="site-preferences">
      <LanguageSwitcher
        :label="t('admin.language')" :current="current" :choices="choices"
        @select="preference = $event"
      />
      <div class="site-theme-control">
        <label for="public-theme">{{ t('admin.theme') }}</label>
        <select id="public-theme" v-model="theme">
          <option v-for="mode in publicThemes" :key="mode" :value="mode">{{ t(`admin.${mode}`) }}</option>
        </select>
      </div>
      <p v-if="current.code !== publication.language.code" class="site-interface-language">
        {{ t('admin.interface_texts') }}
        <span :lang="publication.language.code" :dir="publication.language.direction">{{ publication.language.native_name }}</span>
      </p>
    </div>
  </header>
</template>
