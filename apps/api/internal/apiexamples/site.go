package apiexamples

import (
	_ "embed"
	"encoding/json"
	"maps"

	"stepolonia/apps/api/internal/siteconfig"
)

//go:embed site-ui-pl.json
var demoMessagesJSON []byte

var demoMessages = decodeDemoMessages()

func decodeDemoMessages() map[string]string {
	var messages map[string]string
	if err := json.Unmarshal(demoMessagesJSON, &messages); err != nil {
		panic("invalid embedded demo UI catalog: " + err.Error())
	}
	return messages
}

// DemoSite returns the Polish settings and catalog used in the local public demo.
func DemoSite() siteconfig.Draft {
	return siteconfig.Draft{
		Settings: siteconfig.Settings{Title: "Stepolonia", Description: "Poznaj historie i wydarzenia naszej społeczności.", DefaultLanguage: "pl", FallbackLanguage: "pl", Tagline: "Historie naszej społeczności", Accent: "#2563eb"},
		Catalogs: map[string]map[string]string{"pl": maps.Clone(demoMessages)},
	}
}
