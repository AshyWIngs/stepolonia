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
	settingsPath             = "/api/v1/admin/settings"
	publishSettingsPath      = settingsPath + "/publish"
	readSettingsOperation    = "settingsReadEditable"
	saveSettingsOperation    = "settingsSaveDraft"
	publishSettingsOperation = "settingsPublish"
	settingsBodyBytes        = 1_000_000
	settingsTimeout          = 8 * time.Second
)

var settingsRoutes = map[string][]route{
	settingsPath: {
		{kind: fullKind, method: http.MethodGet, operationID: readSettingsOperation, summary: "Read editable portal settings", response: editableSettingsSchemaName, description: "Requires the settings.manage grant and a verified editor database login. Returns the draft, or the current publication if there is no draft, along with the enabled language directory."},
		{kind: fullKind, method: http.MethodPut, operationID: saveSettingsOperation, summary: "Save a portal settings draft", response: "SettingsVersion", description: "Requires settings.manage, CSRF and fresh strong authentication. Replaces the full draft, with all selected interface catalogs, at the expected version. Public readers keep the previous publication."},
	},
	publishSettingsPath: {
		{kind: fullKind, method: http.MethodPost, operationID: publishSettingsOperation, summary: "Publish portal settings and translations", response: "SettingsPublication", description: "Requires settings.manage, CSRF and fresh strong authentication. Publishes the saved revision at the expected version. Publication and audit commit atomically."},
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
}

type settingsPublicationResponse struct {
	settingsVersion
	PublicationID string `json:"publication_id"`
}

type settingsInput struct {
	ExpectedVersion int64                        `json:"expected_version,string"`
	Settings        siteconfig.Settings          `json:"settings"`
	Catalogs        map[string]map[string]string `json:"catalogs"`
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
	h.changeSettings(ctx, w, req, rt.operationID, input)
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
	})
}

func (h *handler) changeSettings(ctx context.Context, w http.ResponseWriter, req identity.AuthRequest, operation string, input settingsInput) {
	var publication string
	var version int64
	var err error
	if operation == saveSettingsOperation {
		err = h.settings.SaveSiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			version, err = siteconfig.SaveDraftTx(ctx, tx, input.ExpectedVersion, siteconfig.Draft{Settings: input.Settings, Catalogs: input.Catalogs})
			return settingsCommandError(err)
		})
	} else {
		err = h.settings.PublishSiteSettings(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			publication, version, err = siteconfig.PublishTx(ctx, tx, input.ExpectedVersion)
			return settingsCommandError(err)
		})
	}
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	result := settingsVersion{version}
	if operation == saveSettingsOperation {
		respond(w, result)
	} else {
		respond(w, settingsPublicationResponse{settingsVersion: result, PublicationID: publication})
	}
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
