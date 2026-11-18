import { defineNuxtRouteMiddleware, useError, useNuxtApp } from '#imports'
import type { NuxtError } from '#app'
import { isAdminPath } from '~/utils/admin-path'
import { usePublicSite } from '~/composables/usePublicSite'

export default defineNuxtRouteMiddleware(async (to) => {
  if (isAdminPath(to.path)) return
  const app = useNuxtApp()
  const error = useError().value
  const serializedError: Partial<ReturnType<NuxtError['toJSON']>> | undefined = error ?? undefined
  const unavailable = (error?.status ?? serializedError?.statusCode) === 503
  // Nuxt reruns middleware during error rendering and hydration. Retrying the
  // unavailable API could hit the proxy timeout before rendering the 503 response.
  if (unavailable && (import.meta.server || app.isHydrating)) return
  await usePublicSite().load(to.fullPath, import.meta.client && !app.isHydrating)
})
