import { inject, provide, type InjectionKey } from 'vue'
import { useI18n } from '#imports'
import { usePublicSite } from './usePublicSite'
import { compileUiMessage } from '~/utils/ui-message'

function createPublicUi() {
  const publication = usePublicSite().publication.value
  const language = publication?.language.code
  const { t } = useI18n({
    useScope: 'local', inheritLocale: false, locale: language, fallbackLocale: false, flatJson: true,
    messageCompiler: compileUiMessage,
    messages: publication && language ? { [language]: { ...publication.messages } } : {},
  })
  return { publication, t }
}

const pageUi: InjectionKey<ReturnType<typeof createPublicUi>> = Symbol('public-ui')

export function providePublicUi() {
  // Each page keeps its catalog while Nuxt prepares the next page.
  provide(pageUi, createPublicUi())
}

export function usePublicUi() {
  return inject(pageUi, null) ?? createPublicUi()
}
