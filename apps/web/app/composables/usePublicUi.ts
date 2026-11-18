import { computed, watchEffect } from 'vue'
import { useI18n } from '#imports'
import { usePublicSite } from './usePublicSite'

export function usePublicUi() {
  const { publication } = usePublicSite()
  const { locale, fallbackLocale, setLocaleMessage, t } = useI18n()
  const uiLanguage = computed(() => publication.value?.language.code)
  watchEffect(() => {
    const current = publication.value
    if (!current) return
    // Use the published catalog selected by the Go API, including its fallback when needed.
    setLocaleMessage(current.language.code, current.messages)
    fallbackLocale.value = false
    locale.value = current.language.code
  })
  return { uiLanguage, t }
}
