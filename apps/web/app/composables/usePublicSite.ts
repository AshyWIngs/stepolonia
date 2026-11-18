import type { components } from '@stepolonia/sdk'
import { useState } from '#imports'
import { usePublicApi } from './usePublicApi'
import { publicError, routeSegment } from '~/utils/public-content'

type Publication = components['schemas']['Publication']

export function usePublicSite() {
  // useState scopes data to the SSR request and retains the publication during hydration.
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
    try { language = segment ? routeSegment(decodeURIComponent(segment)).toLowerCase() : undefined }
    catch { throw publicError(400) }
    const { client, request } = usePublicApi()
    // A single response keeps settings and the catalog on the same publication.
    try {
      let result = await request(() => client.getSite(language ? { query: { lang: language } } : undefined))
      if (generation.value !== currentGeneration) return
      // The UI API may reject an unknown language code. The content route validates it
      // against the language catalog and returns 404 using the published fallback.
      if (language && result.response.status === 400) result = await request(() => client.getSite())
      const { data, response } = result
      // Keep results from canceled navigation out of the current state.
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
