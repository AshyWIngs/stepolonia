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
	settingsPath           = "/api/v1/admin/settings"
	applySettingsPath      = settingsPath + "/publication"
	readSettingsOperation  = "settingsReadEditable"
	applySettingsOperation = "settingsApply"
	settingsBodyBytes      = 1_000_000
	settingsTimeout        = 8 * time.Second
)

var settingsRoutes = map[string][]route{
	settingsPath: {
		{kind: fullKind, method: http.MethodGet, operationID: readSettingsOperation, summary: "Read editable portal settings", response: editableSettingsSchemaName, description: "Requires the settings.manage grant and a verified editor database login. Returns pending settings when present, otherwise the current publication, along with the enabled language directory."},
	},
	applySettingsPath: {
		{kind: fullKind, method: http.MethodPut, operationID: applySettingsOperation, summary: "Apply portal settings", response: "SettingsPublication", description: "Requires a full session, settings.manage and CSRF. Validates and applies the complete settings and interface catalogs at the expected version. Settings and audit commit atomically. Omitted slug rules keep the editable revision's rules."},
	},
}

type settingsVersion struct {
	Version int64 `json:"version,string"`
}

type editableSettingsResponse struct {
	settingsVersion
	HasDraft    bool                         `json:"has_draft"`
	PublishedID string                       `json:"published_id,omitempty"`
	Settings    siteconfig.Settings          `json:"settings"`
	Catalogs    map[string]map[string]string `json:"catalogs"`
	Languages   []siteconfig.Language        `json:"languages"`
	MessageKeys []string                     `json:"message_keys"`
	SlugRules   []siteconfig.SlugRule        `json:"slug_rules" nullable:"false" doc:"Slug suggestion rules from the same editable revision, ordered by content language code and source character."`
}

type settingsPublicationResponse struct {
	settingsVersion
	PublicationID string `json:"publication_id"`
}

type settingsInput struct {
	ExpectedVersion int64                        `json:"expected_version,string"`
	Settings        siteconfig.Settings          `json:"settings"`
	Catalogs        map[string]map[string]string `json:"catalogs"`
	SlugRules       []siteconfig.SlugRule        `json:"slug_rules"`
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
	raw, code, status := readJSONBody(w, r, settingsBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	var input settingsInput
	if !decodeSettings(raw, settingsRequestSchema(rt.operationID), &input) {
		writeError(w, identity.ErrValidation)
		return
	}
	h.applySettings(ctx, w, req, input)
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
	respond(w, editableSettingsResponse{
		settingsVersion: settingsVersion{editable.Version}, HasDraft: editable.HasDraft, PublishedID: editable.PublishedID,
		Settings: editable.Settings, Catalogs: editable.Catalogs, Languages: editable.Languages, MessageKeys: editable.MessageKeys,
		SlugRules: editable.SlugRules,
	})
}

func (h *handler) applySettings(ctx context.Context, w http.ResponseWriter, req identity.AuthRequest, input settingsInput) {
	var result settingsPublicationResponse
	err := h.settings.ApplySiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		result.PublicationID, result.Version, err = siteconfig.ApplyTx(ctx, tx, input.ExpectedVersion, siteconfig.Draft{
			Settings: input.Settings, Catalogs: input.Catalogs, SlugRules: input.SlugRules,
		})
		return settingsCommandError(err)
	})
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	respond(w, result)
}

func decodeSettings(raw []byte, schema *huma.Schema, input *settingsInput) bool {
	value, ok := decodeUniqueJSON(raw)
	if !ok || !utf8.Valid(raw) || !exactDraftProperties(value, schema) {
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
	if errors.As(err, &domain) && domain.Status == http.StatusConflict {
		problem(w, domain.Status, domain.Code)
		return
	}
	writeError(w, err)
}
