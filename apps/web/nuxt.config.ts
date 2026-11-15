export default defineNuxtConfig({
  compatibilityDate: '2026-11-10',
  devtools: { enabled: false },
  telemetry: false,
  modules: ['@nuxt/eslint', '@nuxtjs/i18n'],
  i18n: {
    strategy: 'no_prefix',
    detectBrowserLanguage: false,
    locales: ['ru', 'pl', 'kk', 'en'],
    vueI18n: './i18n.config.ts',
  },
  typescript: { strict: true },
  nitro: { preset: 'node-server' },
  build: { transpile: ['@stepolonia/sdk'] },
  runtimeConfig: {
    apiOrigin: '',
    public: {
      site: {
        name: 'Stepolonia',
        tagline: '',
        description: '',
        notice: '',
        locale: '',
        uiFallback: '',
        accent: '#a52837',
        origin: '',
      },
    },
  },
  app: {
    head: {
      meta: [{ name: 'robots', content: 'noindex, nofollow' }],
    },
  },
})
