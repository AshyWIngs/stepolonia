import withNuxt from './.nuxt/eslint.config.mjs'

export default withNuxt({
  rules: {
    complexity: ['error', 15],
  },
})
