package adminintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"stepolonia/apps/api/internal/adminhttp"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
)

const settingsEndpoint = "/api/v1/admin/settings"
const applySettingsEndpoint = settingsEndpoint + "/publication"
const settingsSignInMessage = "admin.sign_in"
const settingsInitialPolishSignIn = "Zaloguj się"

type settingsHTTPFixture struct {
	server       *httptest.Server
	owner        *sql.DB
	editorLogin  *sql.DB
	manager      identity.AuthRequest
	withoutGrant identity.AuthRequest
}

func newSettingsHTTPFixture(t *testing.T) settingsHTTPFixture {
	t.Helper()
	ctx := context.Background()
	state := authFixture(t)
	publishTestSiteCatalog(t, state.db)
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
	return settingsHTTPFixture{server: server, owner: state.db, editorLogin: login, manager: authReq(manager), withoutGrant: observer}
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
	SlugRules   []siteconfig.SlugRule        `json:"slug_rules"`
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
	verifySettingsDenials(t, f, settingsSavePayload(t, editable))

	incomplete := f.editable(t)
	delete(incomplete.Catalogs["en"], settingsSignInMessage)
	f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, settingsSavePayload(t, incomplete), http.StatusBadRequest)
	assertPublicSettingsUnchanged(t, f, initialPublic)
	assertSettingsSuccessAudit(t, f.owner, 0, 0)

	// Seed settings left pending before the apply-only editor was introduced.
	const pendingDescription = "Community information"
	editable.Settings.Description = pendingDescription
	editor := siteconfig.NewService(f.editorLogin)
	_, err := editor.SaveDraft(context.Background(), 3, siteconfig.Draft{Settings: editable.Settings, Catalogs: editable.Catalogs})
	requireNoError(t, err)
	pending := f.editable(t)
	if pending.Version != "4" || !pending.HasDraft || pending.Settings.Description != pendingDescription {
		t.Fatalf("missing prior pending changes: %+v", pending)
	}
	editable = f.editable(t)
	editable.Settings.Title = title
	editable.Catalogs["en"][settingsSignInMessage] = signIn
	saveBody := settingsSavePayload(t, editable)
	verifySettingsApplyRollback(t, f, saveBody, pending, initialPublic)
	response := f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, saveBody, http.StatusOK)
	var applied struct {
		Version       string `json:"version"`
		PublicationID string `json:"publication_id"`
	}
	requireNoError(t, json.Unmarshal([]byte(response), &applied))
	published := f.public(t, "en")
	if published.Version == initialPublic.Version || published.Settings.Title != title ||
		published.Settings.Description != pendingDescription || published.Messages[settingsSignInMessage] != signIn ||
		applied.Version != "5" || applied.PublicationID != published.Version || !slices.Equal(published.SlugRules, initialPublic.SlugRules) {
		t.Fatalf("publication did not expose the new title and translation: %+v", published)
	}
	if f.public(t, "pl").Messages[settingsSignInMessage] != settingsInitialPolishSignIn {
		t.Fatal("publication dropped the untouched Polish translation")
	}
	result := f.editable(t)
	if result.Version != "5" || result.HasDraft || result.PublishedID != published.Version {
		t.Fatalf("editable settings did not return to the published state: %+v", result)
	}
	assertSettingsSuccessAudit(t, f.owner, 0, 1)
}

func TestAdminPortalSlugRulesHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	initial := f.public(t, "pl")
	editable := f.editable(t)
	if len(initial.SlugRules) == 0 || !slices.Equal(editable.SlugRules, initial.SlugRules) {
		t.Fatal("initial published and editable rules must match")
	}
	for _, invalid := range []any{nil, []string{"ł"}, []siteconfig.SlugRule{
		{LanguageCode: "pl", Character: "Ą", Replacement: "a"},
		{LanguageCode: "PL", Character: "A\u0328", Replacement: "other"},
	}} {
		f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, settingsSlugRulesPayload(t, editable, invalid), http.StatusBadRequest)
	}
	if current := f.editable(t); current.Version != editable.Version || current.HasDraft || !slices.Equal(current.SlugRules, initial.SlugRules) {
		t.Fatalf("invalid slug rules changed the editable revision: %+v", current)
	}
	assertSettingsSuccessAudit(t, f.owner, 0, 0)
	input := []siteconfig.SlugRule{{LanguageCode: "PL", Character: "A\u0328", Replacement: ""}}
	payload := settingsSlugRulesPayload(t, editable, input)
	f.request(t, f.withoutGrant, http.MethodPut, applySettingsEndpoint, payload, http.StatusForbidden)
	f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, payload, http.StatusOK)
	applied := f.editable(t)
	want := []siteconfig.SlugRule{{LanguageCode: "pl", Character: "ą", Replacement: ""}}
	if applied.HasDraft || applied.Version != "4" || !slices.Equal(applied.SlugRules, want) {
		t.Fatalf("slug rules did not activate in one save: %#v", applied)
	}
	published := f.public(t, "pl")
	if published.Version == initial.Version || !slices.Equal(published.SlugRules, want) {
		t.Fatalf("slug rules were not published with the new revision: %#v", published.SlugRules)
	}
	f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, settingsSlugRulesPayload(t, applied, []siteconfig.SlugRule{}), http.StatusOK)
	if current := f.public(t, "pl"); current.Version == published.Version || len(current.SlugRules) != 0 {
		t.Fatal("an explicit empty array did not clear the rules")
	}
	assertSettingsSuccessAudit(t, f.owner, 0, 2)
}

func settingsSlugRulesPayload(t *testing.T, editable observedEditableSettings, rules any) string {
	t.Helper()
	var payload map[string]any
	requireNoError(t, json.Unmarshal([]byte(settingsSavePayload(t, editable)), &payload))
	payload["slug_rules"] = rules
	body, err := json.Marshal(payload)
	requireNoError(t, err)
	return string(body)
}

func assertInitialEditableSettings(t *testing.T, editable observedEditableSettings, publicationID string) {
	t.Helper()
	if editable.Version != "3" || editable.HasDraft || editable.PublishedID != publicationID ||
		editable.Settings.Title != "Editorial workspace" {
		t.Fatalf("unexpected initial editable settings: %+v", editable)
	}
	if editable.Catalogs["pl"][settingsSignInMessage] != settingsInitialPolishSignIn {
		t.Fatal("editable view does not include the already published Polish catalog")
	}
}

func verifySettingsDenials(t *testing.T, f settingsHTTPFixture, body string) {
	t.Helper()
	f.request(t, f.withoutGrant, http.MethodGet, settingsEndpoint, "", http.StatusForbidden)
	f.request(t, f.withoutGrant, http.MethodPut, applySettingsEndpoint, body, http.StatusForbidden)
	f.request(t, f.manager, http.MethodPut, settingsEndpoint, body, http.StatusMethodNotAllowed)
	f.request(t, f.manager, http.MethodPost, settingsEndpoint+"/publish", `{"expected_version":"3"}`, http.StatusNotFound)
	f.request(t, identity.AuthRequest{}, http.MethodGet, settingsEndpoint, "", http.StatusUnauthorized)

	noCSRF := f.manager
	noCSRF.CSRF = ""
	f.request(t, noCSRF, http.MethodPut, applySettingsEndpoint, body, http.StatusForbidden)
	expireSessionStepUp(t, f.owner, f.manager.Token)

	stale := f.editable(t)
	stale.Version = "2"
	rejected := f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, settingsSavePayload(t, stale), http.StatusConflict)
	assertPublicationProblem(t, rejected, "site_version_conflict")
	if state := f.editable(t); state.Version != "3" || state.HasDraft {
		t.Fatalf("rejected writes changed editable state: %+v", state)
	}
	assertSettingsSuccessAudit(t, f.owner, 0, 0)
}

func verifySettingsApplyRollback(t *testing.T, f settingsHTTPFixture, body string, pending observedEditableSettings, initial siteconfig.Publication) {
	t.Helper()
	_, err := f.owner.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_settings_publish_audit
CHECK (operation <> 'settings.published' OR result <> 'success')`)
	requireNoError(t, err)
	f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, body, http.StatusServiceUnavailable)
	assertPublicSettingsUnchanged(t, f, initial)
	if state := f.editable(t); state.Version != pending.Version || !state.HasDraft ||
		state.PublishedID != pending.PublishedID || state.Settings != pending.Settings ||
		state.Catalogs["en"][settingsSignInMessage] != pending.Catalogs["en"][settingsSignInMessage] {
		t.Fatalf("failed audit did not roll back publication: %+v", state)
	}
	assertSettingsSuccessAudit(t, f.owner, 0, 0)
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
