package adminhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	settingsPath                    = "/api/v1/admin/settings"
	applySettingsPath               = settingsPath + "/publication"
	appearanceSettingsPath          = settingsPath + "/appearance"
	languageFlagSettingsPath        = settingsPath + "/language-flags"
	portalDetailsSettingsPath       = settingsPath + "/details"
	localeDefaultsSettingsPath      = settingsPath + "/locale-defaults"
	slugRulesSettingsPath           = settingsPath + "/slug-rules"
	catalogSettingsPath             = settingsPath + "/catalogs"
	readSettingsOperation           = "settingsReadEditable"
	applySettingsOperation          = "settingsApply"
	appearanceSettingsOperation     = "settingsApplyAppearance"
	languageFlagSettingsOperation   = "settingsApplyLanguageFlag"
	portalDetailsSettingsOperation  = "settingsApplyDetails"
	localeDefaultsSettingsOperation = "settingsApplyLocaleDefaults"
	slugRulesSettingsOperation      = "settingsApplySlugRules"
	catalogSettingsOperation        = "settingsApplyCatalog"
	settingsBodyBytes               = 1_000_000
	settingsTimeout                 = 8 * time.Second
)

var settingsRoutes = map[string][]route{
	settingsPath: {
		{kind: fullKind, method: http.MethodGet, operationID: readSettingsOperation, summary: "Read editable portal settings", response: editableSettingsSchemaName, description: "Requires the settings.manage grant and a verified editor database login. Returns pending settings when present, otherwise the current publication, along with the enabled language directory."},
	},
	applySettingsPath: {
		{kind: fullKind, method: http.MethodPut, operationID: applySettingsOperation, summary: "Apply portal settings", response: "SettingsPublication", description: "Requires a full session, settings.manage and CSRF. Validates and applies the complete settings and interface catalogs at the expected version. Settings and audit commit atomically. Omitted slug rules keep the editable revision's rules."},
	},
	appearanceSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: appearanceSettingsOperation, summary: "Save portal appearance", response: scopedSettingsResponseName, description: "Changes the accent using its own version. Requires a full session, CSRF and settings.manage."},
	},
	languageFlagSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: languageFlagSettingsOperation, summary: "Save a language flag", response: scopedSettingsResponseName, description: "Changes the flag of one enabled language using that flag's version. Requires a full session, CSRF and settings.manage."},
	},
	portalDetailsSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: portalDetailsSettingsOperation, summary: "Save portal details", response: scopedSettingsReceiptName, description: "Changes the portal name, tagline, description and notice using the current details version. Requires a full session, CSRF and settings.manage."},
	},
	localeDefaultsSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: localeDefaultsSettingsOperation, summary: "Save language preferences", response: scopedSettingsReceiptName, description: "Changes the default content language and interface fallback catalog using their preferences version. Requires a full session, CSRF and settings.manage."},
	},
	slugRulesSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: slugRulesSettingsOperation, summary: "Save changes to slug rules", response: scopedSettingsReceiptName, description: "Applies only the added, updated or removed rules at the current rules version. Requires a full session, CSRF and settings.manage."},
	},
	catalogSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: catalogSettingsOperation, summary: "Save one interface catalog", response: scopedSettingsReceiptName, description: "Updates messages or removes one catalog using its saved version. Requires a full session, CSRF and settings.manage. A catalog used as the interface fallback cannot be removed."},
	},
}

type settingsVersion struct {
	Version int64 `json:"version,string"`
}

type editableSettingsResponse struct {
	settingsVersion
	HasDraft              bool                         `json:"has_draft"`
	PublishedID           string                       `json:"published_id,omitempty"`
	Settings              siteconfig.Settings          `json:"settings"`
	Catalogs              map[string]map[string]string `json:"catalogs"`
	Languages             []siteconfig.Language        `json:"languages"`
	MessageKeys           []string                     `json:"message_keys"`
	SlugRules             []siteconfig.SlugRule        `json:"slug_rules" nullable:"false" doc:"Slug suggestion rules from the same editable revision, ordered by content language code and source character."`
	LanguageFlags         map[string]string            `json:"language_flags" nullable:"false" doc:"Static SVG flags for enabled languages in the editable revision. Missing entries use a globe."`
	AppearanceVersion     string                       `json:"appearance_version"`
	DetailsVersion        string                       `json:"details_version"`
	LocaleDefaultsVersion string                       `json:"locale_defaults_version"`
	SlugRulesVersion      string                       `json:"slug_rules_version"`
	CatalogVersions       map[string]string            `json:"catalog_versions" nullable:"false"`
	LanguageFlagVersions  map[string]string            `json:"language_flag_versions" nullable:"false"`
}

type settingsPublicationResponse struct {
	settingsVersion
	PublicationID string            `json:"publication_id"`
	LanguageFlags map[string]string `json:"language_flags,omitempty" doc:"Normalized SVG for languages changed by this request. An empty value means a globe."`
}

type settingsInput struct {
	ExpectedVersion int64                        `json:"expected_version,string"`
	Settings        siteconfig.Settings          `json:"settings"`
	Catalogs        map[string]map[string]string `json:"catalogs"`
	SlugRules       []siteconfig.SlugRule        `json:"slug_rules"`
	LanguageFlags   map[string]string            `json:"language_flags"`
}

func (h *handler) serveSettings(w http.ResponseWriter, r *http.Request, rt route, ip string) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), settingsTimeout)
	defer cancel()
	if rt.method == http.MethodGet {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			writeError(w, identity.ErrValidation)
			return
		}
		h.readSettings(ctx, w, req)
		return
	}
	raw, code, status := readJSONBody(w, r, settingsRequestLimit(rt))
	if code != "" {
		problem(w, status, code)
		return
	}
	if rt.method == http.MethodPost {
		h.applyScopedSettings(ctx, w, req, rt.operationID, raw)
		return
	}
	var input settingsInput
	if !decodeSettings(raw, settingsRequestSchema(rt.operationID), &input) {
		writeError(w, identity.ErrValidation)
		return
	}
	h.applySettings(ctx, w, req, input)
}

func settingsRequestLimit(rt route) int64 {
	if rt.operationID == catalogSettingsOperation {
		return settingsBodyBytes
	}
	if rt.operationID == slugRulesSettingsOperation {
		return maxDraftBodyBytes
	}
	if rt.method == http.MethodPost {
		return 32_768
	}
	return settingsBodyBytes
}

func (h *handler) readSettings(ctx context.Context, w http.ResponseWriter, req identity.AuthRequest) {
	var editable siteconfig.Editable
	err := h.settings.ReadSiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		editable, err = siteconfig.ReadEditable(ctx, tx)
		return settingsCommandError(err)
	})
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	flagVersions := make(map[string]string, len(editable.Languages))
	catalogVersions := make(map[string]string, len(editable.Languages))
	for _, language := range editable.Languages {
		flagVersions[language.Code] = siteconfig.ScopeVersion(editable.LanguageFlags[language.Code])
		catalogVersions[language.Code] = siteconfig.CatalogVersion(nil)
	}
	for code, messages := range editable.Catalogs {
		catalogVersions[code] = siteconfig.CatalogVersion(messages)
	}
	respond(w, editableSettingsResponse{
		settingsVersion: settingsVersion{editable.Version}, HasDraft: editable.HasDraft, PublishedID: editable.PublishedID,
		Settings: editable.Settings, Catalogs: editable.Catalogs, Languages: editable.Languages, MessageKeys: editable.MessageKeys,
		SlugRules: editable.SlugRules, LanguageFlags: editable.LanguageFlags,
		AppearanceVersion: siteconfig.ScopeVersion(editable.Settings.Accent), DetailsVersion: editable.Settings.Details().Version(), LanguageFlagVersions: flagVersions,
		LocaleDefaultsVersion: editable.Settings.LocaleDefaults().Version(), SlugRulesVersion: siteconfig.SlugRulesVersion(editable.SlugRules),
		CatalogVersions: catalogVersions,
	})
}

func (h *handler) applySettings(ctx context.Context, w http.ResponseWriter, req identity.AuthRequest, input settingsInput) {
	var result siteconfig.AppliedRevision
	err := h.settings.ApplySiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		result, err = siteconfig.ApplyTx(ctx, tx, input.ExpectedVersion, siteconfig.Draft{
			Settings: input.Settings, Catalogs: input.Catalogs, SlugRules: input.SlugRules, LanguageFlags: input.LanguageFlags,
		})
		return settingsCommandError(err)
	})
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	respond(w, settingsPublicationResponse{
		settingsVersion: settingsVersion{result.Version}, PublicationID: result.PublicationID, LanguageFlags: result.LanguageFlags,
	})
}

func decodeSettings(raw []byte, schema *huma.Schema, input *settingsInput) bool {
	value, ok := decodeUniqueJSON(raw)
	object, isObject := value.(map[string]any)
	if !ok || !isObject || !utf8.Valid(raw) || !exactDraftProperties(value, schema) {
		return false
	}
	if flags, present := object[languageFlagsField]; present && flags == nil {
		return false
	}
	var result huma.ValidateResult
	huma.Validate(nil, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	return len(result.Errors) == 0 && json.Unmarshal(raw, input) == nil && input.ExpectedVersion > 0
}

func settingsCommandError(err error) error {
	var problem *siteconfig.Error
	if errors.As(err, &problem) {
		switch problem.Status {
		case http.StatusBadRequest:
			return errors.Join(identity.ErrValidation, err)
		case http.StatusForbidden:
			return errors.Join(identity.ErrPermissionDenied, err)
		case http.StatusConflict:
			return errors.Join(identity.ErrVersionConflict, err)
		}
	}
	return err
}

func writeSettingsError(w http.ResponseWriter, err error) {
	var domain *siteconfig.Error
	if errors.As(err, &domain) && (domain.Status == http.StatusConflict || isScopedSettingsValidation(domain.Code)) {
		problem(w, domain.Status, domain.Code)
		return
	}
	writeError(w, err)
}

func isScopedSettingsValidation(code string) bool {
	switch code {
	case siteconfig.InvalidLanguageFlagCode, siteconfig.InvalidAccentCode, siteconfig.InvalidDetailsCode, siteconfig.InvalidLocaleDefaultsCode, siteconfig.InvalidSlugRulesCode, siteconfig.InvalidCatalogChangeCode:
		return true
	default:
		return false
	}
}
