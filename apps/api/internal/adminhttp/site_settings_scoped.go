package adminhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const scopedSettingsResponseName = "ScopedSettingsPublication"
const scopedSettingsReceiptName = "ScopedSettingsReceipt"

type scopedSettingsInput struct {
	ExpectedVersion string                    `json:"expected_version"`
	Accent          string                    `json:"accent"`
	LanguageCode    string                    `json:"language_code"`
	SVG             string                    `json:"svg"`
	Details         siteconfig.PortalDetails  `json:"details"`
	Defaults        siteconfig.LocaleDefaults `json:"defaults"`
	UpsertRules     []siteconfig.SlugRule     `json:"upsert"`
	RemoveRules     []siteconfig.SlugRuleKey  `json:"remove"`
	Messages        map[string]string         `json:"messages"`
	RemoveCatalog   bool                      `json:"remove_catalog"`
}

type scopedSettingsReceipt struct {
	settingsVersion
	PreviousVersion int64  `json:"previous_version,string"`
	PublicationID   string `json:"publication_id"`
	ScopeVersion    string `json:"scope_version"`
}

type scopedSettingsResponse struct {
	scopedSettingsReceipt
	Value string `json:"value"`
}

func (h *handler) applyScopedSettings(ctx context.Context, w http.ResponseWriter, req identity.AuthRequest, operation string, raw []byte) {
	var input scopedSettingsInput
	if !decodeClosedCommand(raw, settingsRequestSchema(operation), &input) {
		writeError(w, identity.ErrValidation)
		return
	}
	var result siteconfig.ScopedPublication
	err := h.settings.ApplySiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		switch operation {
		case appearanceSettingsOperation:
			result, err = siteconfig.ApplyAppearanceTx(ctx, tx, input.ExpectedVersion, input.Accent)
		case portalDetailsSettingsOperation:
			result, err = siteconfig.ApplyPortalDetailsTx(ctx, tx, input.ExpectedVersion, input.Details)
		case localeDefaultsSettingsOperation:
			result, err = siteconfig.ApplyLocaleDefaultsTx(ctx, tx, input.ExpectedVersion, input.Defaults)
		case slugRulesSettingsOperation:
			result, err = siteconfig.ApplySlugRuleChangesTx(ctx, tx, input.ExpectedVersion, siteconfig.SlugRuleChanges{Upsert: input.UpsertRules, Remove: input.RemoveRules})
		case catalogSettingsOperation:
			result, err = siteconfig.ApplyCatalogChangeTx(ctx, tx, input.ExpectedVersion, siteconfig.CatalogChange{Language: input.LanguageCode, Messages: input.Messages, Remove: input.RemoveCatalog})
		default:
			result, err = siteconfig.ApplyLanguageFlagTx(ctx, tx, input.ExpectedVersion, input.LanguageCode, input.SVG)
		}
		return settingsCommandError(err)
	})
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	receipt := scopedSettingsReceipt{settingsVersion{result.Version}, result.PreviousVersion, result.PublicationID, result.ScopeVersion}
	if operation != appearanceSettingsOperation && operation != languageFlagSettingsOperation {
		respond(w, receipt)
		return
	}
	respond(w, scopedSettingsResponse{receipt, result.Value})
}

func decodeClosedCommand(raw []byte, schema *huma.Schema, input any) bool {
	value, ok := decodeUniqueJSON(raw)
	if !ok || !utf8.Valid(raw) || !exactDraftProperties(value, schema) {
		return false
	}
	var result huma.ValidateResult
	huma.Validate(nil, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	return len(result.Errors) == 0 && json.Unmarshal(raw, input) == nil
}
