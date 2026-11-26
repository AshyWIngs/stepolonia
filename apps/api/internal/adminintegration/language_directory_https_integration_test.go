package adminintegration

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const installationLanguagesEndpoint = settingsEndpoint + "/languages"
const fifthLanguageCode = "uk"

type directoryTestDefinition struct {
	Language        siteconfig.Language `json:"language"`
	ExpectedVersion string              `json:"expected_version,omitempty"`
}

type directoryTestCatalog struct {
	ExpectedVersion string            `json:"expected_version"`
	LanguageCode    string            `json:"language_code"`
	Messages        map[string]string `json:"messages,omitempty"`
	Remove          bool              `json:"remove_catalog,omitempty"`
}

func TestAdminInstallationLanguageDirectoryHTTPSIntegration(t *testing.T) {
	f := newSettingsHTTPFixture(t)
	command := directoryTestDefinition{Language: siteconfig.Language{
		Code: "UK", Name: "Ukrainian", NativeName: "Українська", Direction: "ltr",
	}}
	body := languageCommandBody(t, command)
	assertDirectoryDenials(t, f, body)
	initial := f.editable(t)

	entry := f.languageCommand(t, installationLanguagesEndpoint, body)
	if entry.Code != fifthLanguageCode || entry.Enabled || entry.CatalogPublished || entry.Version == "" {
		t.Fatalf("creation did not return a disabled canonical entry: %+v", entry)
	}
	if exported := readDirectoryLanguage(t, f, fifthLanguageCode); exported != entry {
		t.Fatalf("single-record export = %+v, want %+v", exported, entry)
	}
	assertPublicationProblem(t, f.request(t, f.manager, http.MethodPost, installationLanguagesEndpoint, body, http.StatusConflict), settingsVersionConflictProblem)

	exported := f.request(t, f.manager, http.MethodGet, installationLanguagesEndpoint+"/definition?code="+entry.Code, "", http.StatusOK)
	requireNoError(t, json.Unmarshal([]byte(exported), &command))
	if command.ExpectedVersion != entry.Version {
		t.Fatalf("editable export did not carry the current version")
	}
	command.Language.NativeName = "Украї́нська"
	edited := f.languageCommand(t, installationLanguagesEndpoint, languageCommandBody(t, command))
	if edited.Version == entry.Version || edited.NativeName != "Украї́нська" || edited.Enabled {
		t.Fatalf("metadata change did not preserve availability: %+v", edited)
	}
	assertPublicationProblem(t, f.request(t, f.manager, http.MethodPost, installationLanguagesEndpoint, languageCommandBody(t, command), http.StatusConflict), settingsVersionConflictProblem)

	checkDirectoryCatalogLifecycle(t, f, initial, edited)
}

func checkDirectoryCatalogLifecycle(t *testing.T, f settingsHTTPFixture, initial observedEditableSettings, edited siteconfig.LanguageEntry) {
	t.Helper()
	availability := installationLanguagesEndpoint + "/availability"
	active := f.languageCommand(t, availability, languageAvailabilityBody(t, edited, true))
	if !active.Enabled || active.CatalogPublished || active.Version == edited.Version {
		t.Fatalf("enabling should require separate catalog publication: %+v", active)
	}
	if published := f.public(t, fifthLanguageCode); published.Language.Code == fifthLanguageCode {
		t.Fatal("enabled language appeared before publishing its catalog")
	}
	publishInterfaceTranslations(t, f, initial, active)
	catalog := f.public(t, fifthLanguageCode)
	if catalog.Language.Code != fifthLanguageCode || catalog.Language.NativeName != active.NativeName ||
		!slices.ContainsFunc(catalog.Languages, func(item siteconfig.Language) bool { return item.Code == fifthLanguageCode }) {
		t.Fatalf("published settings did not expose the fifth catalog: %+v", catalog.Language)
	}
	assertPublicationProblem(t, f.request(t, f.manager, http.MethodPost, availability, languageAvailabilityBody(t, active, false), http.StatusConflict), "language_used_in_settings")

	command := directoryTestDefinition{Language: active.Language, ExpectedVersion: active.Version}
	command.Language.NativeName = "Українська"
	renamed := f.languageCommand(t, installationLanguagesEndpoint, languageCommandBody(t, command))
	if renamed.NativeName != f.public(t, fifthLanguageCode).Language.NativeName {
		t.Fatal("published metadata did not change with the language definition")
	}
	removeCatalog := directoryTestCatalog{ExpectedVersion: f.editable(t).CatalogVersions[fifthLanguageCode], LanguageCode: fifthLanguageCode, Remove: true}
	f.request(t, f.manager, http.MethodPost, settingsEndpoint+"/catalogs", languageCommandBody(t, removeCatalog), http.StatusOK)
	disabled := f.languageCommand(t, availability, languageAvailabilityBody(t, renamed, false))
	if disabled.Enabled || disabled.CatalogPublished || disabled.Version == renamed.Version {
		t.Fatalf("language remained active after removing its published references: %+v", disabled)
	}
}

func languageCommandBody(t *testing.T, command any) string {
	t.Helper()
	raw, err := json.Marshal(command)
	requireNoError(t, err)
	return string(raw)
}

func languageAvailabilityBody(t *testing.T, item siteconfig.LanguageEntry, enabled bool) string {
	t.Helper()
	return languageCommandBody(t, map[string]any{"code": item.Code, "expected_version": item.Version, "enabled": enabled})
}

func (f settingsHTTPFixture) languageCommand(t *testing.T, endpoint, body string) siteconfig.LanguageEntry {
	t.Helper()
	var item siteconfig.LanguageEntry
	requireNoError(t, json.Unmarshal([]byte(f.request(t, f.manager, http.MethodPost, endpoint, body, http.StatusOK)), &item))
	return item
}

func readDirectoryLanguage(t *testing.T, f settingsHTTPFixture, code string) siteconfig.LanguageEntry {
	t.Helper()
	var result struct {
		Items []siteconfig.LanguageEntry `json:"items"`
	}
	requireNoError(t, json.Unmarshal([]byte(f.request(t, f.manager, http.MethodGet, installationLanguagesEndpoint+"?code="+code, "", http.StatusOK)), &result))
	if len(result.Items) != 1 {
		t.Fatalf("directory returned %d entries for %s", len(result.Items), code)
	}
	return result.Items[0]
}

func publishInterfaceTranslations(t *testing.T, f settingsHTTPFixture, initial observedEditableSettings, entry siteconfig.LanguageEntry) {
	t.Helper()
	current := f.editable(t)
	command := directoryTestCatalog{ExpectedVersion: current.CatalogVersions[entry.Code], LanguageCode: entry.Code, Messages: initial.Catalogs["en"]}
	f.request(t, f.manager, http.MethodPost, settingsEndpoint+"/catalogs", languageCommandBody(t, command), http.StatusOK)
}

func assertDirectoryDenials(t *testing.T, f settingsHTTPFixture, body string) {
	t.Helper()
	f.request(t, identity.AuthRequest{}, http.MethodGet, installationLanguagesEndpoint, "", http.StatusUnauthorized)
	f.request(t, f.withoutGrant, http.MethodGet, installationLanguagesEndpoint, "", http.StatusForbidden)
	f.request(t, f.withoutGrant, http.MethodPost, installationLanguagesEndpoint, body, http.StatusForbidden)
	expireSessionStepUp(t, f.owner, f.manager.Token)
	assertPublicationProblem(t, f.request(t, f.manager, http.MethodPost, installationLanguagesEndpoint, body, http.StatusForbidden), "step_up_required")
	authRefreshStep(t, f.owner, f.manager.Token)
	withoutCSRF := f.manager
	withoutCSRF.CSRF = "wrong-directory-token"
	assertPublicationProblem(t, f.request(t, withoutCSRF, http.MethodPost, installationLanguagesEndpoint, body, http.StatusForbidden), "csrf_failed")
}
