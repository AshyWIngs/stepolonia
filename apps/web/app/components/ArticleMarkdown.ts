import { defineComponent, h } from 'vue'
import { markdownNodes, markdownTokens } from '~/utils/markdown'

export default defineComponent({
  props: { source: { type: String, required: true } },
  setup(props) {
    // Vue экранирует текст и создаёт DOM из разрешённых элементов и атрибутов.
    return () => h('div', { class: 'article-body' }, markdownNodes(markdownTokens(props.source)))
  },
})
