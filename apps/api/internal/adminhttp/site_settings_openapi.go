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
	slugRulesField             = "slug_rules"
)

var applySettingsSchema = newSettingsRequestSchema()

func newSettingsRequestSchema() *huma.Schema {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	settings := huma.SchemaFromType(registry, reflect.TypeFor[siteconfig.Settings]())
	settings.AdditionalProperties = false
	rule := huma.SchemaFromType(registry, reflect.TypeFor[siteconfig.SlugRule]())
	rule.AdditionalProperties = false
	rule.Properties["character"].MaxLength = intPointer(4)
	rule.Properties["character"].Description = "Source must normalize to one non-ASCII Unicode character in lowercase NFC form. Space, control, format, private-use and unassigned characters are excluded."
	properties := map[string]*huma.Schema{
		expectedVersionField: contentVersionSchema("Current settings version.", "1"),
		"settings":           settings,
		"catalogs": {Type: huma.TypeObject, AdditionalProperties: &huma.Schema{
			Type: huma.TypeObject, AdditionalProperties: &huma.Schema{Type: huma.TypeString},
		}},
	}
	required := slices.Sorted(maps.Keys(properties))
	properties[slugRulesField] = &huma.Schema{
		Type: huma.TypeArray, Items: rule, MaxItems: intPointer(siteconfig.MaxSlugRules),
		Description: "When provided, replaces all editable slug suggestion rules. An empty array removes all rules. Omit to preserve the current editable rules. Each normalized language and source pair must be unique.",
	}
	example := map[string]any{
		expectedVersionField: "1",
		"settings":           apiexamples.PortalSettings(),
		"catalogs":           map[string]map[string]string{"pl": apiexamples.PortalMessageExample()},
		slugRulesField:       []siteconfig.SlugRule{slugRuleExample()},
	}
	schema := closedObjectSchema("Apply the complete settings at the expected version. Read editable settings to get the complete catalogs. The example illustrates the shape with one message.", properties, required, example)
	schema.PrecomputeMessages()
	return schema
}

func settingsRequestSchema(operation string) *huma.Schema {
	if operation == applySettingsOperation {
		return applySettingsSchema
	}
	return nil
}

func addSettingsSchemas(schemas map[string]*huma.Schema) {
	settings := apiexamples.PortalSettings()
	language := siteconfig.Language{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: "ltr"}
	messages := apiexamples.PortalMessageExample()
	schemas["Settings"] = responseDTOSchema("Installation title, descriptions, accent and language settings.", settings)
	schemas["Language"] = responseDTOSchema("Enabled language with its native name and text direction.", language)
	schemas["SlugRule"] = responseDTOSchema("A canonical character replacement for slug suggestions in one content language.", slugRuleExample())
	for name, example := range map[string]any{
		"SettingsPublication": settingsPublicationResponse{settingsVersion{2}, exampleUUID},
		editableSettingsSchemaName: editableSettingsResponse{
			settingsVersion: settingsVersion{1},
			Settings:        settings,
			Catalogs:        map[string]map[string]string{language.Code: messages},
			Languages:       []siteconfig.Language{language},
			MessageKeys:     slices.Sorted(maps.Keys(messages)),
			SlugRules:       []siteconfig.SlugRule{slugRuleExample()},
		},
	} {
		description := "The activated revision and version after the transaction commits."
		if name == editableSettingsSchemaName {
			description = "Current editable settings, enabled languages and registered message keys. The example illustrates the catalog shape with one message."
		}
		schema := responseDTOSchema(description, example)
		schema.Properties["version"] = contentVersionSchema("Current settings version.", "1")
		schemas[name] = schema
	}
}

func slugRuleExample() siteconfig.SlugRule {
	return siteconfig.SlugRule{LanguageCode: "pl", Character: "ł", Replacement: "l"}
}

func settingsErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{
		400: {validationCode}, 401: {authenticationCode}, 403: {originCode, permissionCode},
		404: {notFoundCode}, 405: {methodNotAllowedCode}, 503: {securityUnavailableCode},
	}
	if rt.method != http.MethodGet {
		codes[403] = append(codes[403], csrfCode)
		codes[409] = []string{"site_version_conflict"}
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	return codes
}
