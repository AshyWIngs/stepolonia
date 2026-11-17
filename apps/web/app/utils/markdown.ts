import MarkdownIt, { type Token } from 'markdown-it'
import { h, type VNodeChild } from 'vue'

export function markdownTokens(source: string): Token[] {
  return new MarkdownIt({ html: false, linkify: false, typographer: false }).parse(source, {})
}

export function markdownDescription(source: string): string {
  const text = (tokens: Token[]): string => tokens.map(token => token.children ? text(token.children) : token.content).join(' ')
  return text(markdownTokens(source)).replace(/\s+/gu, ' ').trim().slice(0, 200)
}

const allowedMarkdownTags = new Set(['p', 'blockquote', 'ul', 'ol', 'li', 'strong', 'em', 's', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'table', 'thead', 'tbody', 'tr', 'th', 'td'])

function linkUrl(value: string): string | undefined {
  if (!value || /[\s\\\p{Cc}\p{Cf}]/u.test(value) || value.startsWith('//')) return undefined
  try {
    const url = new URL(value, 'https://markdown.invalid')
    return ['https:', 'http:', 'mailto:'].includes(url.protocol) ? value : undefined
  }
  catch {
    return undefined
  }
}

function attributes(token: Token): Record<string, string | number> {
  if (token.tag === 'a') {
    const href = linkUrl(String(token.attrGet('href') ?? ''))
    return href ? { href, rel: 'noopener noreferrer', referrerpolicy: 'no-referrer' } : {}
  }
  const start = String(token.attrGet('start') ?? '')
  return token.tag === 'ol' && start && /^\d+$/u.test(start) ? { start: Number(start) } : {}
}

type Frame = { tag: string, attrs: Record<string, string | number>, children: VNodeChild[] }

export function markdownNodes(tokens: Token[]): VNodeChild[] {
  const root: Frame = { tag: 'div', attrs: {}, children: [] }
  const stack = [root]
  for (const token of tokens) {
    const current = stack.at(-1) ?? root
    if (token.hidden) continue
    if (token.nesting === 1) stack.push({ tag: allowedMarkdownTags.has(token.tag) ? token.tag : 'span', attrs: attributes(token), children: [] })
    else if (token.nesting === -1) {
      const frame = stack.pop()
      if (frame) (stack.at(-1) ?? root).children.push(h(frame.tag, frame.attrs, frame.children))
    }
    else current.children.push(...leafNodes(token))
  }
  return root.children
}

function leafNodes(token: Token): VNodeChild[] {
  if (token.type === 'inline') return markdownNodes(token.children ?? [])
  if (token.type === 'fence' || token.type === 'code_block') return [h('pre', [h('code', token.content)])]
  if (token.type === 'code_inline') return [h('code', token.content)]
  if (token.type === 'hardbreak') return [h('br')]
  if (token.type === 'softbreak') return ['\n']
  if (token.type === 'hr') return [h('hr')]
  // Изображения пока показывают только альтернативный текст, без загрузки внешних медиа.
  return [token.content]
}
