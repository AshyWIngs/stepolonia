import withNuxt from './.nuxt/eslint.config.mjs'
import sonarjs from 'eslint-plugin-sonarjs'

const adminFeatures = [
  { directory: 'portal-settings', entry: 'app/components/PortalSettings.vue' },
  { directory: 'content-registry', entry: 'app/components/AdminTypeBuilder.vue' },
  { directory: 'admin-workspace', entry: 'app/pages/admin.vue' },
  { directory: 'content-draft', entry: 'app/components/AdminDraftEditor.vue' },
]
const sourceFiles = ['**/*.{vue,js,mjs,cjs,jsx,ts,mts,cts,tsx}']

function featureFiles({ directory, entry }) {
  return [`app/features/${directory}/**`, entry]
}

function restrictFeatureSources(name, files, ignores, features) {
  const patterns = features.map(({ directory, entry }) => ({
    regex: `(?:^|/)${directory}(?:/|$)`,
    message: `Keep ${directory} internals behind ${entry}.`,
  }))
  return [{
    name,
    files,
    ignores,
    rules: { 'no-restricted-imports': ['error', { patterns }] },
  }, {
    name: `${name}-sfc`,
    files: files.map(pattern => [pattern, '**/*.vue']),
    ignores,
    rules: {
      'vue/no-restricted-syntax': ['error', ...patterns.map(({ regex, message }) => ({
        selector: `VDocumentFragment > VElement > VStartTag > VAttribute[directive=false][key.name="src"][value.value=/${regex.replaceAll('/', '\\u002f')}/]`,
        message,
      }))],
    },
  }]
}

export default withNuxt({
  plugins: { sonarjs },
  rules: {
    'no-await-in-loop': 'error',
    'vue/multi-word-component-names': 'error',
    complexity: ['error', 15],
    'sonarjs/cognitive-complexity': ['error', 15],
    'max-lines-per-function': ['error', { max: 80, skipBlankLines: true, skipComments: true }],
    'max-statements': ['error', 50],
  },
},
{
  name: 'stepolonia/frontend-file-size',
  files: ['{app,server,tests}/**/*.{vue,js,mjs,cjs,jsx,ts,mts,cts,tsx}'],
  rules: { 'max-lines': ['error', 700] },
},
...restrictFeatureSources('stepolonia/admin-feature-entrances', sourceFiles, adminFeatures.flatMap(featureFiles), adminFeatures),
...adminFeatures.flatMap(feature => restrictFeatureSources(
  `stepolonia/${feature.directory}-dependencies`,
  featureFiles(feature),
  [],
  adminFeatures.filter(other => other !== feature),
)))
