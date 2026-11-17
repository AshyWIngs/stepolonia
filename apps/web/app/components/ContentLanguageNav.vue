<script setup lang="ts">
import type { components } from '@stepolonia/sdk'
import { useContentLanguageCookie } from '~/composables/useContentLanguageCookie'
import { usePublicUi } from '~/composables/usePublicUi'
import { contentPath, type ContentNamespace } from '~/utils/public-content'

defineProps<{
  languages: components['schemas']['LanguageDTO'][]
  namespace: ContentNamespace
  current: string
  translations?: { language: string, slug: string }[]
}>()

const saved = useContentLanguageCookie()
const { t } = usePublicUi()
</script>

<template>
  <nav :aria-label="t('languages')" data-testid="content-languages" class="language-nav">
    <template v-for="language in languages" :key="language.code">
      <NuxtLink
        v-if="!translations || translations.some(item => item.language === language.code)"
        :to="contentPath(namespace, language.code, translations?.find(item => item.language === language.code)?.slug)"
        :lang="language.code"
        :dir="language.direction"
        :data-language="language.code"
        :aria-current="language.code === current ? 'page' : undefined"
        @click="saved = language.code"
      >{{ language.native_name }}</NuxtLink>
    </template>
  </nav>
</template>
