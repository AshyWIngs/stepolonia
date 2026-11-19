import { computed } from 'vue'
import { useCookie, useRequestURL, useState } from '#imports'

export const publicThemes = ['system', 'light', 'dark'] as const

export function usePublicTheme() {
  const preference = useCookie<string | null>('sp_public_theme', {
    path: '/',
    sameSite: 'lax',
    secure: useRequestURL().protocol === 'https:',
    maxAge: 31536000,
  })
  const theme = useState('public-theme', () => publicThemes.find(value => value === preference.value) ?? 'system')
  return computed({
    get: () => theme.value,
    set: value => {
      theme.value = value
      preference.value = value
    },
  })
}
