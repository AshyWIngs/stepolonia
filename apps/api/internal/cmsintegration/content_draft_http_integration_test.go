package cmsintegration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/cmshttp"
	"stepolonia/apps/api/internal/platform"
)

const draftHTTPPrefix = "/api/v1/admin/content/"

func newDraftTLSServer(t *testing.T, f draftEditorFixture) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	auth, err := cmshttp.NewHandlerWithDraftEditor(cmshttp.Config{CanonicalOrigin: "https://" + server.Listener.Addr().String()}, f.auth, f.editor)
	requireNoError(t, err)
	server.Config.Handler = platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	server.StartTLS()
	return server
}

type draftHTTPRequest struct {
	path, body, csrf string
	status           int
}

func sendDraftPatch(t *testing.T, server *httptest.Server, token string, input draftHTTPRequest) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, server.URL+input.path, strings.NewReader(input.body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, server.URL, input.csrf)
	req.AddCookie(&http.Cookie{Name: "__Host-sp_session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	res, err := server.Client().Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	if res.StatusCode != input.status {
		t.Fatalf("PATCH %s: status %d want %d, response %s", input.path, res.StatusCode, input.status, data)
	}
	assertAuthNoStore(t, res)
	return string(data)
}

func TestCMSDraftPatchHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	path := draftHTTPPrefix + "editorial/" + draftEditItem
	const editedHeadline, editedNote = "Edited via HTTPS", "Editor-only update"
	const patch = `{"expected_version":"1","schema_no":"1","shared":{"headline":{"kind":"string","value":"Edited via HTTPS"}}}`
	const writeOnlyPatch = `{"expected_version":"1","schema_no":"1","shared":{"note":{"kind":"string","value":"Editor-only update"}}}`
	sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path, patch, "", http.StatusForbidden})
	expireSessionStepUp(t, f.owner, f.actor.Token)
	sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path, patch, f.actor.CSRF, http.StatusForbidden})
	authRefreshStep(t, f.owner, f.actor.Token)

	cases := []draftHTTPRequest{
		{draftHTTPPrefix + "different/" + draftEditItem, patch, f.actor.CSRF, http.StatusNotFound},
		{draftHTTPPrefix + "editorial/" + draftOtherItem, writeOnlyPatch, f.actor.CSRF, http.StatusForbidden},
		{path, `{"expected_version":"1","schema_no":"1","publication_id":"injected"}`, f.actor.CSRF, http.StatusBadRequest},
	}
	for _, input := range cases {
		sendDraftPatch(t, server, f.actor.Token, input)
	}
	assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
	const update = `{"expected_version":"1","schema_no":"1","shared":{"headline":{"kind":"string","value":"Edited via HTTPS"},"note":{"kind":"string","value":"Editor-only update"}}}`
	out := sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path, update, f.actor.CSRF, http.StatusOK})
	if !strings.Contains(out, `"version":"2"`) || !strings.Contains(out, editedHeadline) || strings.Contains(out, editedNote) || strings.Contains(out, initialDraftNote) {
		t.Fatalf("unexpected draft projection: %s", out)
	}
	assertDraftEditState(t, f.owner, draftEditItem, 2, editedHeadline, editedNote)
	sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path, patch, f.actor.CSRF, http.StatusConflict})
	requireNoError(t, f.auth.Logout(context.Background(), f.actor, fullSessionFixtureKind))
	sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path, patch, f.actor.CSRF, http.StatusUnauthorized})
	assertDraftEditState(t, f.owner, draftEditItem, 2, editedHeadline, editedNote)
}
