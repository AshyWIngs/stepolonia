<script setup lang="ts">
import type { components } from '@stepolonia/sdk'
import { useContentLanguageCookie } from '~/composables/useContentLanguageCookie'
import { usePublicUi } from '~/composables/usePublicUi'
import { articlesPath } from '~/utils/public-content'

defineProps<{
  languages: components['schemas']['LanguageDTO'][]
  current: string
  translations?: { language_code: string, slug: string }[]
}>()

const saved = useContentLanguageCookie()
const { t } = usePublicUi()
</script>

<template>
  <nav :aria-label="t('languages')" data-testid="content-languages" class="language-nav">
    <template v-for="language in languages" :key="language.code">
      <NuxtLink
        v-if="!translations || translations.some(item => item.language_code === language.code)"
        :to="articlesPath(language.code, translations?.find(item => item.language_code === language.code)?.slug)"
        :lang="language.code"
        :dir="language.direction"
        :data-language="language.code"
        :aria-current="language.code === current ? 'page' : undefined"
        @click="saved = language.code"
      >{{ language.native_name }}</NuxtLink>
    </template>
  </nav>
</template>
