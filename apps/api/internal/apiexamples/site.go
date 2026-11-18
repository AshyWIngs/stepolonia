package apiexamples

import (
	_ "embed" // Embeds the demo catalog seed.
	"encoding/json"
	"maps"

	"stepolonia/apps/api/internal/siteconfig"
)

//go:embed site-ui-catalogs.json
var demoMessagesJSON []byte

var demoMessages = decodeDemoMessages()

func decodeDemoMessages() map[string]map[string]string {
	var messages map[string]map[string]string
	if err := json.Unmarshal(demoMessagesJSON, &messages); err != nil {
		panic("invalid embedded demo UI catalog: " + err.Error())
	}
	return messages
}

// DemoSite returns the settings and initial catalogs used in the local demo.
func DemoSite() siteconfig.Draft {
	catalogs := make(map[string]map[string]string, len(demoMessages))
	for code, messages := range demoMessages {
		catalogs[code] = maps.Clone(messages)
	}
	return siteconfig.Draft{
		Settings: siteconfig.Settings{Title: "Polacy w Kazachstanie", Description: "Poznaj historie i wydarzenia naszej społeczności.", DefaultLanguage: "pl", FallbackLanguage: "pl", Tagline: "Historie naszej społeczności", Accent: "#2563eb"},
		Catalogs: catalogs,
	}
}
