package adminintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"stepolonia/apps/api/internal/adminhttp"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
)

const settingsEndpoint = "/api/v1/admin/settings"
const settingsPublishEndpoint = settingsEndpoint + "/publish"
const settingsSignInMessage = "admin.sign_in"
const settingsInitialFrenchSignIn = "Connexion à la rédaction"

type settingsHTTPFixture struct {
	server       *httptest.Server
	owner        *sql.DB
	manager      identity.AuthRequest
	withoutGrant identity.AuthRequest
}

func newSettingsHTTPFixture(t *testing.T) settingsHTTPFixture {
	t.Helper()
	ctx := context.Background()
	state := authFixture(t)
	publishAdminBrowserCatalog(t, state.db)
	manager := authFull(t, state)
	role := fixturePublicationRole(t, state.db, "Support observer")
	observer := fixturePublicationAccount(t, state, manager, "support-observer@example.test", role)
	login := openDraftEditorLogin(t, state.db)
	editor, err := database.NewDraftEditor(ctx, login, state.config)
	requireNoError(t, err)
	auth, err := identity.NewAuthService(login, state.config)
	requireNoError(t, err)
	reader := siteconfig.NewReader(openPublicationRuntimeLogin(t, state.db))
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	adapter, err := adminhttp.NewHandlerWithEditors(adminhttp.Config{CanonicalOrigin: "https://" + server.Listener.Addr().String()}, auth, editor, auth)
	requireNoError(t, err)
	server.Config.Handler = platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), adapter, reader)
	server.StartTLS()
	return settingsHTTPFixture{server, state.db, authReq(manager), observer}
}

func (f settingsHTTPFixture) request(t *testing.T, actor identity.AuthRequest, method, endpoint, body string, status int) string {
	t.Helper()
	return sendDraftRequest(t, f.server, method, actor.Token, draftHTTPRequest{endpoint, body, actor.CSRF, status})
}

type observedEditableSettings struct {
	Version     string                       `json:"version"`
	HasDraft    bool                         `json:"has_draft"`
	PublishedID string                       `json:"published_id"`
	Settings    siteconfig.Settings          `json:"settings"`
	Catalogs    map[string]map[string]string `json:"catalogs"`
}

func (f settingsHTTPFixture) editable(t *testing.T) observedEditableSettings {
	t.Helper()
	body := f.request(t, f.manager, http.MethodGet, settingsEndpoint, "", http.StatusOK)
	var data observedEditableSettings
	requireNoError(t, json.Unmarshal([]byte(body), &data))
	return data
}

func (f settingsHTTPFixture) public(t *testing.T, language string) siteconfig.Publication {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.server.URL+"/api/v1/site?lang="+language, nil)
	requireNoError(t, err)
	response, err := f.server.Client().Do(req)
	requireNoError(t, err)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("public settings returned %d: %s", response.StatusCode, body)
	}
	var result siteconfig.Publication
	requireNoError(t, json.NewDecoder(response.Body).Decode(&result))
	return result
}

func settingsSavePayload(t *testing.T, data observedEditableSettings) string {
	t.Helper()
	body, err := json.Marshal(struct {
		ExpectedVersion string                       `json:"expected_version"`
		Settings        siteconfig.Settings          `json:"settings"`
		Catalogs        map[string]map[string]string `json:"catalogs"`
	}{data.Version, data.Settings, data.Catalogs})
	requireNoError(t, err)
	return string(body)
}

func TestAdminPortalSettingsHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	const title = "Polacy w Kazachstanie"
	const signIn = "Enter the community portal"
	initialPublic := f.public(t, "en")
	editable := f.editable(t)
	assertInitialEditableSettings(t, editable, initialPublic.Version)
	editable.Settings.Title = title
	editable.Catalogs["en"][settingsSignInMessage] = signIn
	saveBody := settingsSavePayload(t, editable)
	verifySettingsDenials(t, f, saveBody)

	f.request(t, f.manager, http.MethodPut, settingsEndpoint, saveBody, http.StatusOK)
	draft := f.editable(t)
	if draft.Version != "4" || !draft.HasDraft || draft.Settings.Title != title || draft.PublishedID != initialPublic.Version ||
		draft.Catalogs["en"][settingsSignInMessage] != signIn || draft.Catalogs["fr"][settingsSignInMessage] != settingsInitialFrenchSignIn {
		t.Fatalf("unexpected persisted draft: %+v", draft)
	}
	assertPublicSettingsUnchanged(t, f, initialPublic)
	verifySettingsPublishRollback(t, f, initialPublic)
	f.request(t, f.manager, http.MethodPost, settingsPublishEndpoint, `{"expected_version":"4"}`, http.StatusOK)
	published := f.public(t, "en")
	if published.Version == initialPublic.Version || published.Settings.Title != title || published.Messages[settingsSignInMessage] != signIn {
		t.Fatalf("publication did not expose the new title and translation: %+v", published)
	}
	if f.public(t, "fr").Messages[settingsSignInMessage] != settingsInitialFrenchSignIn {
		t.Fatal("publication dropped the untouched French translation")
	}
	result := f.editable(t)
	if result.Version != "5" || result.HasDraft || result.PublishedID != published.Version {
		t.Fatalf("editable settings did not return to the published state: %+v", result)
	}
	assertSettingsSuccessAudit(t, f.owner, 1, 1)
}

func assertInitialEditableSettings(t *testing.T, editable observedEditableSettings, publicationID string) {
	t.Helper()
	if editable.Version != "3" || editable.HasDraft || editable.PublishedID != publicationID ||
		editable.Settings.Title != "Editorial workspace" {
		t.Fatalf("unexpected initial editable settings: %+v", editable)
	}
	if editable.Catalogs["fr"][settingsSignInMessage] != settingsInitialFrenchSignIn {
		t.Fatal("editable view does not include the already published French catalog")
	}
}

func verifySettingsDenials(t *testing.T, f settingsHTTPFixture, body string) {
	t.Helper()
	f.request(t, f.withoutGrant, http.MethodGet, settingsEndpoint, "", http.StatusForbidden)
	f.request(t, f.withoutGrant, http.MethodPut, settingsEndpoint, body, http.StatusForbidden)
	f.request(t, f.withoutGrant, http.MethodPost, settingsPublishEndpoint, `{"expected_version":"3"}`, http.StatusForbidden)
	f.request(t, identity.AuthRequest{}, http.MethodGet, settingsEndpoint, "", http.StatusUnauthorized)

	noCSRF := f.manager
	noCSRF.CSRF = ""
	f.request(t, noCSRF, http.MethodPut, settingsEndpoint, body, http.StatusForbidden)
	expireSessionStepUp(t, f.owner, f.manager.Token)
	staleAuth := f.request(t, f.manager, http.MethodPut, settingsEndpoint, body, http.StatusForbidden)
	assertPublicationProblem(t, staleAuth, "step_up_required")
	authRefreshStep(t, f.owner, f.manager.Token)

	stale := f.editable(t)
	stale.Version = "2"
	rejected := f.request(t, f.manager, http.MethodPut, settingsEndpoint, settingsSavePayload(t, stale), http.StatusConflict)
	assertPublicationProblem(t, rejected, "site_version_conflict")
	if state := f.editable(t); state.Version != "3" || state.HasDraft {
		t.Fatalf("rejected writes changed editable state: %+v", state)
	}
	assertSettingsSuccessAudit(t, f.owner, 0, 0)
}

func verifySettingsPublishRollback(t *testing.T, f settingsHTTPFixture, initial siteconfig.Publication) {
	t.Helper()
	_, err := f.owner.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_settings_publish_audit
CHECK (operation <> 'settings.published' OR result <> 'success')`)
	requireNoError(t, err)
	f.request(t, f.manager, http.MethodPost, settingsPublishEndpoint, `{"expected_version":"4"}`, http.StatusServiceUnavailable)
	assertPublicSettingsUnchanged(t, f, initial)
	if state := f.editable(t); state.Version != "4" || !state.HasDraft {
		t.Fatalf("failed audit did not roll back publication: %+v", state)
	}
	assertSettingsSuccessAudit(t, f.owner, 1, 0)
	_, err = f.owner.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_settings_publish_audit`)
	requireNoError(t, err)
}

func assertPublicSettingsUnchanged(t *testing.T, f settingsHTTPFixture, initial siteconfig.Publication) {
	t.Helper()
	if current := f.public(t, "en"); current.Version != initial.Version || current.Settings.Title != initial.Settings.Title ||
		current.Messages[settingsSignInMessage] != initial.Messages[settingsSignInMessage] {
		t.Fatalf("public settings exposed an unpublished draft: %+v", current)
	}
}

func assertSettingsSuccessAudit(t *testing.T, db *sql.DB, saved, published int) {
	t.Helper()
	var saves, publications int
	requireNoError(t, db.QueryRow(`SELECT
 count(*) FILTER (WHERE operation='settings.draft_saved'),
 count(*) FILTER (WHERE operation='settings.published')
 FROM public.security_audit WHERE result='success'`).Scan(&saves, &publications))
	if saves != saved || publications != published {
		t.Fatalf("settings audit counts = %d/%d, want %d/%d", saves, publications, saved, published)
	}
}
