import { computed } from 'vue'
import { useCookie, useRequestURL } from '#imports'

export const publicThemes = ['system', 'light', 'dark'] as const

export function usePublicTheme() {
  const preference = useCookie<string | null>('sp_public_theme', {
    path: '/',
    sameSite: 'lax',
    secure: useRequestURL().protocol === 'https:',
    maxAge: 31536000,
  })
  return computed({
    get: () => publicThemes.find(theme => theme === preference.value) ?? 'system',
    set: theme => { preference.value = theme },
  })
}
