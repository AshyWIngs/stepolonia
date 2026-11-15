import { computed, watchEffect } from 'vue'
import { useI18n, useRoute, useRuntimeConfig } from '#imports'
import { isUiLocale, uiFallback } from '~/utils/language'
import { publicError } from '~/utils/public-content'

export function usePublicUi() {
  const { site } = useRuntimeConfig().public
  const fallback = uiFallback(site)
  if (!fallback) throw publicError(503)
  const route = useRoute()
  const { locale, fallbackLocale, t } = useI18n()
  fallbackLocale.value = fallback
  const uiLanguage = computed(() => {
    const code = typeof route.params.lang === 'string' ? route.params.lang.toLowerCase() : ''
    return isUiLocale(code) ? code : fallback
  })
  watchEffect(() => { locale.value = uiLanguage.value })
  return { uiLanguage, t }
}
