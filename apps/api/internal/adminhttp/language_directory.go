package adminhttp

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	languageDirectoryPath           = settingsPath + "/languages"
	languageDefinitionPath          = languageDirectoryPath + "/definition"
	readLanguageDefinitionOperation = "settingsExportLanguageDefinition"
	languageDefinitionSchema        = "InstallationLanguageDefinition"
	languageAvailabilityPath        = languageDirectoryPath + "/availability"
	readLanguagesOperation          = "settingsExportLanguages"
	saveLanguageOperation           = "settingsSaveLanguage"
	languageAvailabilityOperation   = "settingsSetLanguageAvailability"
	languageDirectorySchema         = "InstallationLanguageDirectory"
	languageEntrySchema             = "InstallationLanguage"
	languageCodeField               = "code"
	languageDefinitionField         = "language"
)

type languageDirectoryResponse struct {
	Items []siteconfig.LanguageEntry `json:"items" nullable:"false" doc:"Matching language entries. Disabled entries are included."`
}

type languageSaveInput struct {
	ExpectedVersion string              `json:"expected_version,omitempty"`
	Language        siteconfig.Language `json:"language"`
}

type languageDefinitionResponse struct {
	ExpectedVersion string              `json:"expected_version"`
	Language        siteconfig.Language `json:"language"`
}

type languageAvailabilityInput struct {
	ExpectedVersion string `json:"expected_version"`
	Code            string `json:"code"`
	Enabled         bool   `json:"enabled" doc:"The requested availability." example:"true"`
}

func isLanguageMutation(operation string) bool {
	return operation == saveLanguageOperation || operation == languageAvailabilityOperation
}

func (h *handler) readLanguageDirectory(ctx context.Context, w http.ResponseWriter, r *http.Request, req identity.AuthRequest, definition bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	codes, filtered := query[languageCodeField]
	if err != nil || definition && !filtered || len(query) > 0 && (!filtered || len(query) != 1 || len(codes) != 1 || codes[0] == "") {
		writeError(w, identity.ErrValidation)
		return
	}
	var out languageDirectoryResponse
	err = h.settings.ReadSiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		out.Items, err = siteconfig.ReadLanguageDirectory(ctx, tx, query.Get(languageCodeField))
		return settingsCommandError(err)
	})
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	if definition {
		if len(out.Items) == 0 {
			writeError(w, identity.ErrNotFound)
			return
		}
		item := out.Items[0]
		respond(w, languageDefinitionResponse{ExpectedVersion: item.Version, Language: item.Language})
		return
	}
	respond(w, out)
}

func (h *handler) changeLanguageDirectory(ctx context.Context, w http.ResponseWriter, req identity.AuthRequest, operation string, raw []byte) {
	var mutate func(context.Context, *sql.Tx) (siteconfig.LanguageEntry, error)
	if operation == saveLanguageOperation {
		var input languageSaveInput
		if decodeClosedCommand(raw, languageSaveSchema, &input) {
			mutate = func(ctx context.Context, tx *sql.Tx) (siteconfig.LanguageEntry, error) {
				return siteconfig.SaveLanguageTx(ctx, tx, input.ExpectedVersion, input.Language)
			}
		}
	} else {
		var input languageAvailabilityInput
		if decodeClosedCommand(raw, languageAvailabilityRequestSchema, &input) {
			mutate = func(ctx context.Context, tx *sql.Tx) (siteconfig.LanguageEntry, error) {
				return siteconfig.SetLanguageAvailabilityTx(ctx, tx, input.Code, input.ExpectedVersion, input.Enabled)
			}
		}
	}
	if mutate == nil {
		writeError(w, identity.ErrValidation)
		return
	}
	var item siteconfig.LanguageEntry
	err := h.settings.ApplyLanguageDirectory(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		item, err = mutate(ctx, tx)
		return settingsCommandError(err)
	})
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	respond(w, item)
}

var languageExample = siteconfig.Language{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: "ltr"}

func languageCodeSchema() *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, MinLength: intPointer(2), MaxLength: intPointer(63), Description: "BCP 47 code; stored in lowercase canonical form.", Examples: []any{languageExample.Code}}
}

func languageVersionSchema() *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: scopeVersionPattern, Description: "Current version from the directory entry. Omit it only when creating a language.", Examples: []any{siteconfig.ScopeVersion("pl")}}
}

var languageSaveSchema = newLanguageSaveSchema()

func newLanguageSaveSchema() *huma.Schema {
	schema := responseDTOSchema("Import a language definition or replace its metadata.", languageSaveInput{Language: languageExample})
	language := responseDTOSchema("Language definition shared with the export response.", languageExample)
	language.Properties[languageCodeField] = languageCodeSchema()
	for _, field := range []string{"name", "native_name"} {
		language.Properties[field].MinLength = intPointer(1)
		language.Properties[field].MaxLength = intPointer(siteconfig.MaxLanguageNameLength)
	}
	// Strict body decoding checks exact property names recursively before validation.
	schema.Properties[languageDefinitionField] = language
	schema.Properties[expectedVersionField] = languageVersionSchema()
	schema.PrecomputeMessages()
	return schema
}

var languageAvailabilityRequestSchema = newLanguageAvailabilitySchema()

func newLanguageAvailabilitySchema() *huma.Schema {
	example := languageAvailabilityInput{ExpectedVersion: siteconfig.ScopeVersion("pl"), Code: languageExample.Code, Enabled: true}
	schema := responseDTOSchema("Change the availability of a saved language.", example)
	schema.Properties[languageCodeField] = languageCodeSchema()
	schema.Properties[expectedVersionField] = languageVersionSchema()
	schema.PrecomputeMessages()
	return schema
}

func addLanguageDirectorySchemas(schemas map[string]*huma.Schema) {
	entry := siteconfig.LanguageEntry{Language: languageExample, Enabled: true, CatalogPublished: true, Version: siteconfig.ScopeVersion("pl")}
	export := responseDTOSchema(
		"A language definition and its edit version. Older CSV records with oversized names or control characters can be exported and should be corrected before import.",
		languageDefinitionResponse{ExpectedVersion: entry.Version, Language: entry.Language})
	export.Properties[languageDefinitionField] = schemaRef("Language")
	export.Properties[expectedVersionField] = languageVersionSchema()
	schemas[languageDefinitionSchema] = export

	item := responseDTOSchema("One language with its transferable definition, availability and edit version.", entry)
	item.Properties[languageDefinitionField] = schemaRef("Language")
	schemas[languageEntrySchema] = item

	directory := responseDTOSchema("Installation language entries ordered by normalized code.", languageDirectoryResponse{[]siteconfig.LanguageEntry{entry}})
	directory.Properties["items"].Items = schemaRef(languageEntrySchema)
	schemas[languageDirectorySchema] = directory
}
