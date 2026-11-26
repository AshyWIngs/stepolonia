package adminintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	uiEntryEndpoint   = "/api/v1/admin/ui/entry"
	uiCatalogEndpoint = "/api/v1/admin/ui/catalog"
)

func TestAdminPublishedUIAudiencesHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	assertVisitorProjection(t, f)
	assertUIProjections(t, f)
	assertUIAccess(t, f)
}

func assertVisitorProjection(t *testing.T, f settingsHTTPFixture) {
	t.Helper()
	visitor := readSettingsPublication[map[string]json.RawMessage](t, f.server, "/api/v1/site?lang=pl")
	var visitorMessages map[string]string
	requireNoError(t, json.Unmarshal(visitor["messages"], &visitorMessages))
	if _, present := visitor["slug_rules"]; present || len(visitorMessages) == 0 {
		t.Fatal("visitor response must contain visitor messages and omit slug rules")
	}
	for key := range visitorMessages {
		if strings.HasPrefix(key, "admin.") {
			t.Fatalf("visitor catalog contains %q", key)
		}
	}
}

func assertUIProjections(t *testing.T, f settingsHTTPFixture) {
	t.Helper()
	entryJSON := readSettingsPublication[map[string]json.RawMessage](t, f.server, uiEntryEndpoint+"?lang=pl")
	for _, field := range []string{"slug_rules", "default_language", "settings", "account_id"} {
		if _, present := entryJSON[field]; present {
			t.Fatalf("entry response exposes %q", field)
		}
	}
	entry := f.entry(t, "PL")
	fallback := readSettingsPublication[siteconfig.AdminEntry](t, f.server, uiEntryEndpoint)
	unavailable := f.entry(t, "fr")
	workspace := f.catalog(t, "pl")
	if fallback.Language.Code != unavailable.Language.Code || entry.Language.Code != "pl" {
		t.Fatal("entry did not preserve locale normalization and fallback")
	}
	if entry.Version != workspace.Version || entry.Version != f.public(t, "pl").Version ||
		workspace.Language.Code != "pl" || len(workspace.SlugRules) == 0 {
		t.Fatal("UI audiences differ in publication or localized content")
	}
	for _, key := range []string{"admin.sign_in", "admin.sign_in_retry", "admin.code_help", "admin.entry_invitation", "admin.entry_recovery"} {
		if entry.Messages[key] == "" || workspace.Messages[key] != entry.Messages[key] {
			t.Fatalf("entry message %q is missing or differs from the workspace", key)
		}
	}
	if workspace.Messages["admin.leave_unsaved"] == "" || entry.Messages["admin.leave_unsaved"] != "" || entry.Messages["articles"] != "" {
		t.Fatal("entry and workspace messages crossed audiences")
	}
}

func assertUIAccess(t *testing.T, f settingsHTTPFixture) {
	t.Helper()
	f.request(t, identity.AuthRequest{}, http.MethodGet, uiCatalogEndpoint, "", http.StatusUnauthorized)
	raw := f.request(t, f.withoutGrant, http.MethodGet, uiCatalogEndpoint+"?lang=pl", "", http.StatusOK)
	var observer struct {
		siteconfig.AdminCatalog
		AccountID string `json:"account_id"`
	}
	requireNoError(t, json.Unmarshal([]byte(raw), &observer))
	if observer.AccountID == "" || observer.Messages["admin.leave_unsaved"] == "" {
		t.Fatal("full session must identify the current account and include workspace translations")
	}
	if observer.SlugRules == nil || len(observer.SlugRules) != 0 {
		t.Fatal("full session without editorial grants must have translations and no slug suggestions")
	}
	assertAdminUICatalogOrigin(t, f)
	for _, query := range []string{"?", "?lang=", "?lang=pl&lang=en", "?lang=pl&", "?version=bad", "?lang=pl_PL"} {
		path := uiEntryEndpoint + query
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.server.URL+path, nil)
		requireNoError(t, err)
		response, err := f.server.Client().Do(req)
		requireNoError(t, err)
		checkAdminUIResponse(t, response, http.StatusBadRequest)
		f.request(t, f.manager, http.MethodGet, uiCatalogEndpoint+query, "", http.StatusBadRequest)
	}
	f.request(t, f.manager, http.MethodPost, uiCatalogEndpoint, "{}", http.StatusMethodNotAllowed)
}

func assertAdminUICatalogOrigin(t *testing.T, f settingsHTTPFixture) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.server.URL+uiCatalogEndpoint, nil)
	requireNoError(t, err)
	request.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: f.manager.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	request.Header.Set("Origin", "https://unrelated.example.test")
	response, err := f.server.Client().Do(request)
	requireNoError(t, err)
	checkAdminUIResponse(t, response, http.StatusForbidden)
}

func checkAdminUIResponse(t *testing.T, response *http.Response, status int) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	assertAuthNoStore(t, response)
	if response.StatusCode != status || len(response.Cookies()) != 0 {
		t.Fatalf("%s: status=%d want=%d, set-cookie=%t", response.Request.URL.Path, response.StatusCode, status, len(response.Cookies()) != 0)
	}
}
