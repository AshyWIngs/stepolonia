package adminhttp

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/siteconfig"
	"stepolonia/apps/api/internal/staticsvg"
)

func settingsRequestDescription(rt route) string {
	version := "Versions are canonical positive int64 strings."
	if rt.method == http.MethodPost {
		version = "Use the current scoped version for the affected section."
	}
	return fmt.Sprintf("One JSON object up to %d bytes. %s", settingsRequestLimit(rt), version)
}

const (
	settingsTag                = "portal-settings"
	editableSettingsSchemaName = "EditablePortalSettings"
	slugRulesField             = "slug_rules"
	languageFlagsField         = "language_flags"
	exampleAccent              = "#2563eb"
)

var applySettingsSchema = newSettingsRequestSchema()
var appearanceSettingsSchema = newScopedSettingsSchema(false)
var languageFlagSettingsSchema = newScopedSettingsSchema(true)
var portalDetailsSchema = scopedSettingsObjectSchema("details", apiexamples.PortalSettings().Details())
var localeDefaultsSchema = scopedSettingsObjectSchema("defaults", apiexamples.PortalSettings().LocaleDefaults())
var slugRuleChangesSchema = newSlugRuleChangesSchema()
var catalogChangeSchema = newCatalogChangeSchema()

const catalogRemoveField = "remove_catalog"
const catalogMessagesField = "messages"
const savedSettingsValueField = "value"

func newCatalogChangeSchema() *huma.Schema {
	messages := &huma.Schema{Type: huma.TypeObject, MinProperties: new(1),
		AdditionalProperties: &huma.Schema{Type: huma.TypeString, MinLength: new(1)},
		Description:          "Changed message keys and the complete new text for each."}
	remove := &huma.Schema{Type: huma.TypeBoolean, Enum: []any{true}, Description: "Remove this catalog."}
	const languageField = "language_code"
	schema := closedObjectSchema("Save one locale's message changes or remove its catalog.", map[string]*huma.Schema{
		expectedVersionField: registryScopeVersionSchema("Current version of the selected catalog."),
		languageField:        {Type: huma.TypeString, MinLength: new(2), MaxLength: new(63)},
		catalogMessagesField: messages,
		catalogRemoveField:   remove,
	}, []string{expectedVersionField, languageField}, map[string]any{
		expectedVersionField: siteconfig.CatalogVersion(nil), languageField: "pl", catalogMessagesField: apiexamples.PortalMessageExample(),
	})
	schema.OneOf = []*huma.Schema{
		{Type: huma.TypeObject, Properties: map[string]*huma.Schema{catalogMessagesField: messages}, Required: []string{catalogMessagesField}},
		{Type: huma.TypeObject, Properties: map[string]*huma.Schema{catalogRemoveField: remove}, Required: []string{catalogRemoveField}},
	}
	schema.PrecomputeMessages()
	return schema
}

func scopedSettingsObjectSchema(key string, example any) *huma.Schema {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	fields := huma.SchemaFromType(registry, reflect.TypeOf(example))
	fields.AdditionalProperties = false
	schema := closedObjectSchema("Apply the selected settings section at its current version.", map[string]*huma.Schema{
		expectedVersionField: registryScopeVersionSchema("Current version of this settings section."),
		key:                  fields,
	}, []string{expectedVersionField, key}, map[string]any{
		expectedVersionField: siteconfig.ScopeVersion(""), key: example,
	})
	schema.PrecomputeMessages()
	return schema
}

func newSlugRuleChangesSchema() *huma.Schema {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	rule := huma.SchemaFromType(registry, reflect.TypeFor[siteconfig.SlugRule]())
	rule.AdditionalProperties = false
	rule.Properties["character"].MaxLength = new(4)
	key := huma.SchemaFromType(registry, reflect.TypeFor[siteconfig.SlugRuleKey]())
	key.AdditionalProperties = false
	schema := closedObjectSchema("Apply changes to slug rules by language and source character.", map[string]*huma.Schema{
		expectedVersionField: registryScopeVersionSchema("Current slug rules version."),
		upsertField:          {Type: huma.TypeArray, Items: rule, MinItems: new(1), MaxItems: new(siteconfig.MaxSlugRules)},
		removeField:          {Type: huma.TypeArray, Items: key, MinItems: new(1), MaxItems: new(siteconfig.MaxSlugRules)},
	}, []string{expectedVersionField}, map[string]any{
		expectedVersionField: siteconfig.SlugRulesVersion(nil), upsertField: []siteconfig.SlugRule{slugRuleExample()},
		removeField: []siteconfig.SlugRuleKey{{LanguageCode: "pl", Character: "ś"}},
	})
	schema.MinProperties = new(2)
	schema.PrecomputeMessages()
	return schema
}

func newScopedSettingsSchema(flag bool) *huma.Schema {
	version := siteconfig.ScopeVersion("")
	properties := map[string]*huma.Schema{
		expectedVersionField: {Type: huma.TypeString, Pattern: "^[0-9a-f]{64}$", Description: "Current scoped version from editable settings."},
	}
	example := map[string]any{expectedVersionField: version}
	if flag {
		properties["language_code"] = &huma.Schema{Type: huma.TypeString, MinLength: intPointer(2), MaxLength: intPointer(63)}
		properties["svg"] = &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(staticsvg.MaxBytes), Description: "Static SVG, or an empty string to use the standard globe."}
		example["language_code"] = "pl"
		example["svg"] = ""
	} else {
		properties["accent"] = &huma.Schema{Type: huma.TypeString, Pattern: "^#[0-9a-fA-F]{6}$"}
		example["accent"] = exampleAccent
	}
	schema := closedObjectSchema("Apply one saved setting using its current scoped version.", properties, slices.Sorted(maps.Keys(properties)), example)
	schema.PrecomputeMessages()
	return schema
}

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
	properties[languageFlagsField] = &huma.Schema{
		Type:                 huma.TypeObject,
		AdditionalProperties: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(staticsvg.MaxBytes)},
		Description:          "Optional flag updates keyed by enabled language code. An empty SVG uses a globe. Omitted languages retain their flags. Only static SVG shapes up to 4096 bytes are accepted.",
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
	switch operation {
	case applySettingsOperation:
		return applySettingsSchema
	case appearanceSettingsOperation:
		return appearanceSettingsSchema
	case languageFlagSettingsOperation:
		return languageFlagSettingsSchema
	case portalDetailsSettingsOperation:
		return portalDetailsSchema
	case localeDefaultsSettingsOperation:
		return localeDefaultsSchema
	case slugRulesSettingsOperation:
		return slugRuleChangesSchema
	case catalogSettingsOperation:
		return catalogChangeSchema
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
		scopedSettingsReceiptName: scopedSettingsReceipt{
			settingsVersion: settingsVersion{2}, PreviousVersion: 1, PublicationID: exampleUUID, ScopeVersion: settings.Details().Version(),
		},
		scopedSettingsResponseName: scopedSettingsResponse{
			scopedSettingsReceipt: scopedSettingsReceipt{settingsVersion: settingsVersion{2}, PreviousVersion: 1, PublicationID: exampleUUID, ScopeVersion: siteconfig.ScopeVersion(exampleAccent)},
			Value:                 exampleAccent,
		},
		"SettingsPublication": settingsPublicationResponse{settingsVersion: settingsVersion{2}, PublicationID: exampleUUID},
		editableSettingsSchemaName: editableSettingsResponse{
			settingsVersion:       settingsVersion{1},
			Settings:              settings,
			Catalogs:              map[string]map[string]string{language.Code: messages},
			Languages:             []siteconfig.Language{language},
			MessageKeys:           slices.Sorted(maps.Keys(messages)),
			SlugRules:             []siteconfig.SlugRule{slugRuleExample()},
			LanguageFlags:         map[string]string{},
			AppearanceVersion:     siteconfig.ScopeVersion(settings.Accent),
			DetailsVersion:        settings.Details().Version(),
			LocaleDefaultsVersion: settings.LocaleDefaults().Version(),
			SlugRulesVersion:      siteconfig.SlugRulesVersion(nil),
			CatalogVersions:       map[string]string{language.Code: siteconfig.CatalogVersion(messages)},
			LanguageFlagVersions:  map[string]string{language.Code: siteconfig.ScopeVersion("")},
		},
	} {
		description := "The activated revision and version after the transaction commits."
		if name == editableSettingsSchemaName {
			description = "Current editable settings, enabled languages and registered message keys. The example illustrates the catalog shape with one message."
		}
		schema := responseDTOSchema(description, example)
		if out, ok := example.(scopedSettingsResponse); ok {
			schema = responseDTOSchema(description, out.scopedSettingsReceipt)
			schema.Properties[savedSettingsValueField] = &huma.Schema{Type: huma.TypeString, Description: "Saved accent or normalized flag SVG."}
			schema.Required = append(schema.Required, savedSettingsValueField)
			schema.Examples = []any{example}
		}
		schema.Properties["version"] = contentVersionSchema("Current settings version.", "1")
		if name == scopedSettingsResponseName || name == scopedSettingsReceiptName {
			schema.Properties["previous_version"] = contentVersionSchema("Settings version immediately before the change.", "1")
		}
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
	switch rt.operationID {
	case applySettingsOperation, languageFlagSettingsOperation:
		codes[400] = append(codes[400], siteconfig.InvalidLanguageFlagCode)
	case appearanceSettingsOperation:
		codes[400] = append(codes[400], siteconfig.InvalidAccentCode)
	case portalDetailsSettingsOperation:
		codes[400] = append(codes[400], siteconfig.InvalidDetailsCode)
	case localeDefaultsSettingsOperation:
		codes[400] = append(codes[400], siteconfig.InvalidLocaleDefaultsCode)
	case slugRulesSettingsOperation:
		codes[400] = append(codes[400], siteconfig.InvalidSlugRulesCode)
	case catalogSettingsOperation:
		codes[400] = append(codes[400], siteconfig.InvalidCatalogChangeCode)
		codes[409] = append(codes[409], siteconfig.CatalogRegistryChangedCode)
	}
	if rt.method == http.MethodPost {
		codes[409] = append(codes[409], siteconfig.PendingDraftCode)
	}
	return codes
}
