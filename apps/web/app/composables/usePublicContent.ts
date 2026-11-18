import { usePublicApi } from './usePublicApi'
import { usePublicSite } from './usePublicSite'
import { contentRedirect, contentPath, contentType, contentPageSize, publicError, type ContentNamespace } from '~/utils/public-content'

export function usePublicContent() {
  const { client, request } = usePublicApi()
  const { publication } = usePublicSite()

  async function listLanguages() {
    if (!publication.value) throw publicError(503)
    const { data, response } = await request(() => client.listLanguages())
    if (!data) throw publicError(response.status)
    if (!data.some(item => item.code === publication.value?.settings.default_language)) throw publicError(503)
    return data
  }

  async function language(code: string) {
    const languages = await listLanguages()
    const selected = languages.find(item => item.code === code)
    if (!selected) throw publicError(404)
    return { selected, languages }
  }

  async function list(namespace: ContentNamespace, code: string, offset: number) {
    const { selected, languages } = await language(code)
    const { data, response } = await request(() => client.listContent({ path: { type_key: contentType(namespace) }, query: { language: selected.code, limit: contentPageSize, offset } }))
    if (!data) throw publicError(response.status)
    return { items: data.items, nextOffset: data.next_offset, language: selected, languages }
  }

  async function detail(namespace: ContentNamespace, code: string, slug: string) {
    const { selected, languages } = await language(code)
    const result = await request(() => client.getContent({ path: { type_key: contentType(namespace), slug }, query: { language: selected.code } }))
    const source = contentPath(namespace, selected.code, slug)
    // Manual redirects hide Location in browsers. Navigate normally so the server handles the 301.
    if (import.meta.client && result.response.type === 'opaqueredirect') {
      return { kind: 'redirect' as const, path: source }
    }
    if (result.response.status === 301) {
      return { kind: 'redirect' as const, path: contentRedirect(result.response.headers.get('location'), namespace, selected.code, source) }
    }
    if (!result.data) throw publicError(result.response.status)
    return { kind: 'content' as const, item: result.data, language: selected, languages }
  }

  return { list, detail, listLanguages }
}
