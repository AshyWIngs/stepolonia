package adminhttp

import (
	"maps"
	"net/http"
	"reflect"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	settingsTag                = "portal-settings"
	editableSettingsSchemaName = "EditablePortalSettings"
)

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
		settings := huma.SchemaFromType(huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer), reflect.TypeFor[siteconfig.Settings]())
		settings.AdditionalProperties = false
		properties["settings"] = settings
		properties["catalogs"] = &huma.Schema{Type: huma.TypeObject, AdditionalProperties: &huma.Schema{
			Type: huma.TypeObject, AdditionalProperties: &huma.Schema{Type: huma.TypeString},
		}}
		required = append(required, "settings", "catalogs")
		example["settings"] = apiexamples.PortalSettings()
		example["catalogs"] = map[string]map[string]string{"pl": apiexamples.PortalMessageExample()}
	}
	description := "Publish the draft at the expected settings version."
	if save {
		description = "Replace the draft at the expected settings version. Read editable settings to get the complete catalogs. The example illustrates the shape with one message."
	}
	schema := closedObjectSchema(description, properties, required, example)
	schema.PrecomputeMessages()
	return schema
}

func settingsRequestSchema(operation string) *huma.Schema {
	return settingsRequestSchemas[operation]
}

func addSettingsSchemas(schemas map[string]*huma.Schema) {
	settings := apiexamples.PortalSettings()
	language := siteconfig.Language{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: "ltr"}
	messages := apiexamples.PortalMessageExample()
	schemas["Settings"] = responseDTOSchema("Installation title, descriptions, accent and language settings.", settings)
	schemas["Language"] = responseDTOSchema("Enabled language with its native name and text direction.", language)
	for name, example := range map[string]any{
		"SettingsVersion":     settingsVersion{2},
		"SettingsPublication": settingsPublicationResponse{settingsVersion{3}, exampleUUID},
		editableSettingsSchemaName: editableSettingsResponse{
			settingsVersion: settingsVersion{1},
			Settings:        settings,
			Catalogs:        map[string]map[string]string{language.Code: messages},
			Languages:       []siteconfig.Language{language},
			MessageKeys:     slices.Sorted(maps.Keys(messages)),
		},
	} {
		description := "Portal settings state or committed command result. The version guards the next edit or publication."
		if name == editableSettingsSchemaName {
			description = "Current draft or publication, enabled languages and registered message keys. The example illustrates the catalog shape with one message."
		}
		schema := responseDTOSchema(description, example)
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
