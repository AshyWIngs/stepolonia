import withNuxt from './.nuxt/eslint.config.mjs'
import sonarjs from 'eslint-plugin-sonarjs'

export default withNuxt({
  plugins: { sonarjs },
  rules: {
    complexity: ['error', 15],
    'sonarjs/cognitive-complexity': ['error', 15],
    'max-lines-per-function': ['error', { max: 80, skipBlankLines: true, skipComments: true }],
    'max-statements': ['error', 50],
  },
})
