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
    // Go уже выбрал опубликованный каталог или DB fallback; старые сообщения не участвуют в поиске.
    setLocaleMessage(current.language.code, current.messages)
    fallbackLocale.value = false
    locale.value = current.language.code
  })
  return { uiLanguage, t }
}
