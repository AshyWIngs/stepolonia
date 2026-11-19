import { useCookie, useRequestHeader, useRequestURL, useState } from '#imports'

export function usePortalAcceptLanguage() {
  // Serialize the initial header for later client-side navigation.
  return useState('portal-accept-language', () => useRequestHeader('accept-language') ?? '')
}

export function usePortalLanguagePreference() {
  return useCookie<string | null>('sp_content_language', {
    path: '/',
    sameSite: 'lax',
    secure: useRequestURL().protocol === 'https:',
    maxAge: 31536000,
  })
}
