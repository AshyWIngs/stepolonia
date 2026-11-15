export default defineNuxtConfig({
  compatibilityDate: '2026-11-10',
  devtools: { enabled: false },
  telemetry: false,
  modules: ['@nuxt/eslint'],
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
        locale: 'ru',
        accent: '#a52837',
        origin: '',
      },
    },
  },
  app: {
    head: {
      htmlAttrs: { lang: 'ru' },
      meta: [{ name: 'robots', content: 'noindex, nofollow' }],
    },
  },
})
