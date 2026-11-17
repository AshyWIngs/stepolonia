import { defineComponent, h } from 'vue'
import { markdownNodes, markdownTokens } from '~/utils/markdown'

export default defineComponent({
  props: { source: { type: String, required: true } },
  setup(props) {
    // Vue escapes text and creates only the permitted elements and attributes.
    return () => h('div', { class: 'article-body' }, markdownNodes(markdownTokens(props.source)))
  },
})
