package apiexamples

import "stepolonia/apps/api/internal/siteconfig"

// DemoSiteSettings returns the branding for the local demonstration portal.
func DemoSiteSettings() siteconfig.Settings {
	return siteconfig.Settings{
		Title:            "Polacy w Kazachstanie",
		Tagline:          "Historie naszej społeczności",
		Description:      "Poznaj historie i wydarzenia naszej społeczności.",
		DefaultLanguage:  "pl",
		FallbackLanguage: "pl",
	}
}

// PortalSettings is a synthetic OpenAPI example.
func PortalSettings() siteconfig.Settings {
	settings := DemoSiteSettings()
	settings.Accent = "#1d4ed8"
	return settings
}

// PortalMessageExample illustrates the catalog shape with one message.
func PortalMessageExample() map[string]string {
	return map[string]string{"articles": "Materiały"}
}
