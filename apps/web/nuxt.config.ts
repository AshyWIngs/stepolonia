export default defineNuxtConfig({
  compatibilityDate: '2026-11-10',
  devtools: { enabled: false },
  telemetry: false,
  typescript: { strict: true },
  nitro: { preset: 'node-server' },
  runtimeConfig: {
    public: {
      site: {
        name: 'Stepolonia',
        tagline: '',
        description: '',
        notice: '',
        locale: 'ru',
        accent: '#a52837',
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
