import { defineNuxtRouteMiddleware, useError, useNuxtApp } from '#imports'
import type { NuxtError } from '#app'
import { usePublicSite } from '~/composables/usePublicSite'

export default defineNuxtRouteMiddleware(async (to) => {
  const app = useNuxtApp()
  const error = useError().value
  const serializedError: Partial<ReturnType<NuxtError['toJSON']>> | undefined = error ?? undefined
  const unavailable = (error?.status ?? serializedError?.statusCode) === 503
  // Nuxt повторяет middleware при SSR ошибки и её гидрации. Повторный запрос
  // недоступного API удвоил бы timeout до предела внешнего proxy вместо готового 503.
  if (unavailable && (import.meta.server || app.isHydrating)) return
  await usePublicSite().load(to.fullPath, import.meta.client && !app.isHydrating)
})
