import { defineNuxtRouteMiddleware, useNuxtApp } from '#imports'
import { usePublicSite } from '~/composables/usePublicSite'

export default defineNuxtRouteMiddleware(async (to) => {
  await usePublicSite().load(to.fullPath, import.meta.client && !useNuxtApp().isHydrating)
})
