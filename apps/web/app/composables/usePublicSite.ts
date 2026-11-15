import type { components } from '@stepolonia/sdk'
import { useState } from '#imports'
import { usePublicApi } from './usePublicApi'
import { publicError } from '~/utils/public-content'

type Publication = components['schemas']['Publication']

export function usePublicSite() {
  // useState изолирует установки и SSR-запросы; payload сохраняет ту же публикацию при гидрации.
  const publication = useState<Publication | null>('public-site', () => null)
  const loadedPath = useState<string | null>('public-site-path', () => null)
  const generation = useState('public-site-generation', () => 0)
  async function load(path: string, refresh = false) {
    if (!refresh && publication.value && loadedPath.value === path) return
    const currentGeneration = ++generation.value
    publication.value = null
    loadedPath.value = null
    const segment = path.split('?')[0]?.split('/')[1]
    let language: string | undefined
    try { language = segment ? decodeURIComponent(segment).toLowerCase() : undefined }
    catch { throw publicError(400) }
    const { client, request } = usePublicApi()
    // Один атомарный ответ исключает смешивание настроек и каталога разных публикаций.
    try {
      const { data, response } = await request(() => client.getSite(language ? { query: { lang: language } } : undefined))
      // Ответ или ошибка отменённой навигации не заменяют состояние нового перехода.
      if (generation.value !== currentGeneration) return
      if (!data) throw publicError(response.status)
      publication.value = data
      loadedPath.value = path
    }
    catch (error) {
      if (generation.value !== currentGeneration) return
      throw error
    }
  }
  return { publication, load }
}
