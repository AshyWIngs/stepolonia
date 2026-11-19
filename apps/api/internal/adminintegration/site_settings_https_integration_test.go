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
const scopedTestAccent = "#774411"

type appearanceCommand struct {
	ExpectedVersion string `json:"expected_version"`
	Accent          string `json:"accent"`
}

type flagCommand struct {
	ExpectedVersion string `json:"expected_version"`
	LanguageCode    string `json:"language_code"`
	SVG             string `json:"svg"`
}

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
	Version               string                       `json:"version"`
	HasDraft              bool                         `json:"has_draft"`
	PublishedID           string                       `json:"published_id"`
	Settings              siteconfig.Settings          `json:"settings"`
	Catalogs              map[string]map[string]string `json:"catalogs"`
	SlugRules             []siteconfig.SlugRule        `json:"slug_rules"`
	AppearanceVersion     string                       `json:"appearance_version"`
	DetailsVersion        string                       `json:"details_version"`
	LocaleDefaultsVersion string                       `json:"locale_defaults_version"`
	SlugRulesVersion      string                       `json:"slug_rules_version"`
	CatalogVersions       map[string]string            `json:"catalog_versions"`
	LanguageFlagVersions  map[string]string            `json:"language_flag_versions"`
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

func TestAdminPortalRetiredCatalogKeyHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	ctx := context.Background()
	const retired = "admin.retired_notice"
	_, err := f.owner.ExecContext(ctx, `INSERT INTO public.ui_message_keys(message_key,placeholders) VALUES($1,'{}')`, retired)
	requireNoError(t, err)
	editable := f.editable(t)
	for _, catalog := range editable.Catalogs {
		catalog[retired] = "Community notice"
	}
	f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, settingsSavePayload(t, editable), http.StatusOK)
	_, err = f.owner.ExecContext(ctx, `DELETE FROM public.ui_message_keys WHERE message_key=$1`, retired)
	requireNoError(t, err)
	if f.public(t, "en").Messages[retired] != "Community notice" {
		t.Fatal("retiring the key changed the current publication")
	}
	editable = f.editable(t)
	for code, catalog := range editable.Catalogs {
		if _, present := catalog[retired]; present {
			t.Fatalf("editable catalog for %s contains a retired key", code)
		}
	}
	f.request(t, f.manager, http.MethodPut, applySettingsEndpoint, settingsSavePayload(t, editable), http.StatusOK)
	if _, present := f.public(t, "en").Messages[retired]; present {
		t.Fatal("the new publication contains a retired key")
	}
}

func TestAdminScopedPortalSettingsHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	initial := f.public(t, "pl")
	editable := f.editable(t)
	flag := `<svg viewBox="0 0 30 20"><rect width="30" height="20" fill="#808080"/></svg>`
	flagPayload := marshalScopedSettings(t, flagCommand{editable.LanguageFlagVersions["pl"], "pl", flag})
	appearancePayload := marshalScopedSettings(t, appearanceCommand{editable.AppearanceVersion, scopedTestAccent})
	details := editable.Settings.Details()
	details.Title = "Dom spotkań polonijnych"
	detailsPayload := marshalScopedSettings(t, struct {
		ExpectedVersion string                   `json:"expected_version"`
		Details         siteconfig.PortalDetails `json:"details"`
	}{editable.DetailsVersion, details})
	const flagEndpoint = settingsEndpoint + "/language-flags"
	const appearanceEndpoint = settingsEndpoint + "/appearance"
	f.request(t, f.withoutGrant, http.MethodPost, flagEndpoint, flagPayload, http.StatusForbidden)
	f.request(t, f.manager, http.MethodPost, flagEndpoint, flagPayload, http.StatusOK)
	afterFlag := f.public(t, "pl")
	if afterFlag.Version == initial.Version || afterFlag.LanguageFlags["pl"] == initial.LanguageFlags["pl"] {
		t.Fatal("flag change was not published")
	}
	f.request(t, f.manager, http.MethodPost, appearanceEndpoint, appearancePayload, http.StatusOK)
	f.request(t, f.manager, http.MethodPost, settingsEndpoint+"/details", detailsPayload, http.StatusOK)
	result := f.public(t, "pl")
	if result.Settings.Accent != scopedTestAccent || result.LanguageFlags["pl"] != afterFlag.LanguageFlags["pl"] || result.Settings.Title != details.Title {
		t.Fatal("publication did not preserve the three independent changes")
	}
	for _, version := range []string{afterFlag.Version, result.Version} {
		var messages int
		requireNoError(t, f.owner.QueryRow(`SELECT count(*) FROM public.site_revision_messages WHERE revision_id=$1`, version).Scan(&messages))
		if messages != 0 {
			t.Fatalf("scoped revision %s copied %d messages", version, messages)
		}
	}
	if result.Messages[settingsSignInMessage] != initial.Messages[settingsSignInMessage] {
		t.Fatal("scoped publication changed the existing catalog")
	}
	beforeConflict := f.public(t, "en")
	f.request(t, f.manager, http.MethodPost, flagEndpoint, flagPayload, http.StatusConflict)
	assertPublicSettingsUnchanged(t, f, beforeConflict)
	assertSettingsSuccessAudit(t, f.owner, 0, 3)
}

func marshalScopedSettings(t *testing.T, input any) string {
	t.Helper()
	body, err := json.Marshal(input)
	requireNoError(t, err)
	return string(body)
}

func TestAdminScopedCatalogHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	initial := f.editable(t)
	const catalogPath = settingsEndpoint + "/catalogs"
	type command struct {
		ExpectedVersion string            `json:"expected_version"`
		LanguageCode    string            `json:"language_code"`
		Messages        map[string]string `json:"messages"`
	}
	polish := command{initial.CatalogVersions["pl"], "pl", map[string]string{settingsSignInMessage: "Wejdź do portalu"}}
	english := command{initial.CatalogVersions["en"], "en", map[string]string{settingsSignInMessage: "Enter the portal"}}
	f.request(t, f.manager, http.MethodPost, catalogPath, marshalScopedSettings(t, polish), http.StatusOK)
	assertOnlyOwnedCatalog(t, f, f.public(t, "pl").Version, "pl")
	f.request(t, f.manager, http.MethodPost, catalogPath, marshalScopedSettings(t, english), http.StatusOK)
	published := f.public(t, "en")
	if published.Messages[settingsSignInMessage] != english.Messages[settingsSignInMessage] ||
		f.public(t, "pl").Messages[settingsSignInMessage] != polish.Messages[settingsSignInMessage] {
		t.Fatal("consecutive catalog changes did not preserve independent locales")
	}
	assertOnlyOwnedCatalog(t, f, published.Version, "en")
	f.request(t, f.manager, http.MethodPost, catalogPath, marshalScopedSettings(t, polish), http.StatusConflict)

	_, err := f.owner.Exec(`INSERT INTO public.ui_message_keys(message_key,placeholders) VALUES('admin.new_catalog_message','{}')`)
	requireNoError(t, err)
	english.ExpectedVersion = f.editable(t).CatalogVersions["en"]
	afterDrift := f.request(t, f.manager, http.MethodPost, catalogPath, marshalScopedSettings(t, english), http.StatusConflict)
	assertPublicationProblem(t, afterDrift, siteconfig.CatalogRegistryChangedCode)
	assertPublicSettingsUnchanged(t, f, published)
	assertSettingsSuccessAudit(t, f.owner, 0, 2)
}

func assertOnlyOwnedCatalog(t *testing.T, f settingsHTTPFixture, revision, language string) {
	t.Helper()
	var owned, inherited, target int
	requireNoError(t, f.owner.QueryRow(`SELECT
 count(*) FILTER (WHERE catalog_revision_id IS NULL),
 count(*) FILTER (WHERE catalog_revision_id IS NOT NULL),
 count(*) FILTER (WHERE language_code=$2 AND catalog_revision_id IS NULL)
 FROM public.site_revision_languages WHERE revision_id=$1`, revision, language).Scan(&owned, &inherited, &target))
	if owned != 1 || inherited == 0 || target != 1 {
		t.Fatalf("publication did not preserve unrelated catalog sources for %q", language)
	}
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
	verifyScopedSettingsPending(t, f, pending)
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

func verifyScopedSettingsPending(t *testing.T, f settingsHTTPFixture, pending observedEditableSettings) {
	t.Helper()
	command := marshalScopedSettings(t, appearanceCommand{pending.AppearanceVersion, scopedTestAccent})
	blocked := f.request(t, f.manager, http.MethodPost, settingsEndpoint+"/appearance", command, http.StatusConflict)
	var conflict struct {
		Code string `json:"code"`
	}
	requireNoError(t, json.Unmarshal([]byte(blocked), &conflict))
	if conflict.Code != "site_draft_pending" {
		t.Fatalf("unexpected scoped conflict code: %q", conflict.Code)
	}
	if kept := f.editable(t); kept.Version != pending.Version || kept.Settings.Description != pending.Settings.Description {
		t.Fatal("the scoped command changed the pending server draft")
	}
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
