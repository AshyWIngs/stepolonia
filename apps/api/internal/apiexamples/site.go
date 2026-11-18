package apiexamples

import "stepolonia/apps/api/internal/siteconfig"

// DemoSite returns the Polish settings and catalog used in the local public demo.
func DemoSite() siteconfig.Draft {
	messages := map[string]string{
		"articles": "Artykuły", "events": "Wydarzenia", "empty": "Brak wyników", "pagination": "Strony",
		"previous": "Poprzednia", "next": "Następna", "navigation": "Nawigacja", "all": "Wszystkie", "languages": "Języki",
		"errors.400": "Nieprawidłowe żądanie", "errors.404": "Nie znaleziono", "errors.503": "Usługa niedostępna",
		"errors.other": "Wystąpił błąd", "errors.retry": "Spróbuj ponownie", "errors.home": "Strona główna",
	}
	return siteconfig.Draft{
		Settings: siteconfig.Settings{Title: "Stepolonia", Description: "Poznaj historie i wydarzenia naszej społeczności.", DefaultLanguage: "pl", FallbackLanguage: "pl", Tagline: "Historie naszej społeczności", Accent: "#2563eb"},
		Catalogs: map[string]map[string]string{"pl": messages},
	}
}
