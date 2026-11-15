import { useCookie, useRequestURL } from '#imports'

export function useContentLanguageCookie() {
  return useCookie<string | null>('sp_content_language', {
    path: '/',
    sameSite: 'lax',
    secure: useRequestURL().protocol === 'https:',
    maxAge: 31536000,
  })
}
