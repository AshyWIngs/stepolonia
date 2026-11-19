import { computed } from 'vue'
import { useCookie, useRequestURL, useState } from '#imports'

export const themeModes = ['system', 'light', 'dark'] as const
export type ThemeMode = typeof themeModes[number]

export function usePublicTheme() {
  const preference = useCookie<string | null>('sp_public_theme', {
    path: '/',
    sameSite: 'lax',
    secure: useRequestURL().protocol === 'https:',
    maxAge: 31536000,
  })
  const theme = useState('public-theme', () => themeModes.find(value => value === preference.value) ?? 'system')
  return computed({
    get: () => theme.value,
    set: value => {
      theme.value = value
      preference.value = value
    },
  })
}
