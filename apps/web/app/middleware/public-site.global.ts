import { defineNuxtRouteMiddleware, useError, useNuxtApp } from '#imports'
import { usePublicSite } from '~/composables/usePublicSite'

export default defineNuxtRouteMiddleware(async (to) => {
  const app = useNuxtApp()
  // Nuxt повторяет middleware при SSR ошибки и её гидрации. Повторный запрос
  // недоступного API удвоил бы timeout до предела внешнего proxy вместо готового 503.
  if (useError().value && (import.meta.server || app.isHydrating)) return
  await usePublicSite().load(to.fullPath, import.meta.client && !app.isHydrating)
})
