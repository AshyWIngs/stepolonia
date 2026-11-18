package adminhttp

import (
	"net/http"
	"reflect"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/siteconfig"
)

const settingsTag = "portal-settings"

var settingsRequestSchemas = map[string]*huma.Schema{
	saveSettingsOperation:    newSettingsRequestSchema(true),
	publishSettingsOperation: newSettingsRequestSchema(false),
}

func newSettingsRequestSchema(save bool) *huma.Schema {
	example := map[string]any{expectedVersionField: "1"}
	properties := map[string]*huma.Schema{
		expectedVersionField: contentVersionSchema("Current settings version.", "1"),
	}
	required := []string{expectedVersionField}
	if save {
		demo := apiexamples.DemoSite()
		settings := huma.SchemaFromType(huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer), reflect.TypeFor[siteconfig.Settings]())
		settings.AdditionalProperties = false
		properties["settings"] = settings
		properties["catalogs"] = &huma.Schema{Type: huma.TypeObject, AdditionalProperties: &huma.Schema{
			Type: huma.TypeObject, AdditionalProperties: &huma.Schema{Type: huma.TypeString},
		}}
		required = append(required, "settings", "catalogs")
		example["settings"] = demo.Settings
		example["catalogs"] = demo.Catalogs
	}
	schema := closedObjectSchema("Versioned portal settings command. Submit the latest editable version and the complete replacement when saving a draft.", properties, required, example)
	schema.PrecomputeMessages()
	return schema
}

func settingsRequestSchema(operation string) *huma.Schema {
	return settingsRequestSchemas[operation]
}

func addSettingsSchemas(schemas map[string]*huma.Schema) {
	demo := apiexamples.DemoSite()
	schemas["Settings"] = responseDTOSchema("Installation title, descriptions, accent and language settings.", demo.Settings)
	schemas["Language"] = responseDTOSchema("Enabled language with its native name and text direction.", siteconfig.Language{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: "ltr"})
	keys := make([]string, 0, len(demo.Catalogs["pl"]))
	for key := range demo.Catalogs["pl"] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for name, example := range map[string]any{
		"SettingsVersion":     settingsVersion{2},
		"SettingsPublication": settingsPublicationResponse{settingsVersion{3}, exampleUUID},
		"EditablePortalSettings": editableSettingsResponse{
			settingsVersion: settingsVersion{1}, Settings: demo.Settings, Catalogs: demo.Catalogs,
			Languages:   []siteconfig.Language{{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: "ltr"}},
			MessageKeys: keys,
		},
	} {
		schema := responseDTOSchema("Portal settings state or committed command result. The version guards the next edit or publication.", example)
		schema.Properties["version"] = contentVersionSchema("Current settings version.", "1")
		schemas[name] = schema
	}
}

func settingsErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{
		400: {validationCode}, 401: {authenticationCode}, 403: {originCode, permissionCode},
		404: {notFoundCode}, 405: {methodNotAllowedCode}, 503: {securityUnavailableCode},
	}
	if rt.method != http.MethodGet {
		codes[403] = append(codes[403], csrfCode, stepUpRequiredCode)
		codes[409] = []string{"site_version_conflict", "site_draft_missing"}
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	return codes
}
