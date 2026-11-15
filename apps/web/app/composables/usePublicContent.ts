import { usePublicApi } from './usePublicApi'
import { usePublicSite } from './usePublicSite'
import { articleRedirect, articlesPath, publicError } from '~/utils/public-content'

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

  async function list(code: string, offset: number) {
    const { selected, languages } = await language(code)
    const { data, response } = await request(() => client.listArticles({ query: { lang: selected.code, limit: 20, offset } }))
    if (!data) throw publicError(response.status)
    return { articles: data, language: selected, languages }
  }

  async function article(code: string, slug: string) {
    const { selected, languages } = await language(code)
    const result = await request(() => client.getArticle({ path: { slug }, query: { lang: selected.code } }))
    const source = articlesPath(selected.code, slug)
    // Браузер скрывает Location при manual redirect. Полный переход возвращает обработку 301 серверу.
    if (import.meta.client && result.response.type === 'opaqueredirect') {
      return { kind: 'redirect' as const, path: source }
    }
    if (result.response.status === 301) {
      return { kind: 'redirect' as const, path: articleRedirect(result.response.headers.get('location'), selected.code, source) }
    }
    if (!result.data) throw publicError(result.response.status)
    return { kind: 'article' as const, article: result.data, language: selected, languages }
  }

  return { list, article, listLanguages }
}
