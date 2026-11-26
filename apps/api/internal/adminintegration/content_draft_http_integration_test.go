package adminintegration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/adminhttp"
	"stepolonia/apps/api/internal/platform"
)

const draftHTTPPrefix = "/api/v1/admin/content/"
const draftEditorialPath = draftHTTPPrefix + "editorial/"

func newDraftTLSServer(t *testing.T, f draftEditorFixture) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	auth, err := adminhttp.NewHandlerWithDraftEditor(adminhttp.Config{CanonicalOrigin: "https://" + server.Listener.Addr().String()}, f.auth, f.editor)
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
	return sendDraftRequest(t, server, http.MethodPatch, token, input)
}

func sendDraftRequest(t *testing.T, server *httptest.Server, method, token string, input draftHTTPRequest) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, server.URL+input.path, strings.NewReader(input.body))
	requireNoError(t, err)
	if method == http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Referer", server.URL+"/admin")
	} else {
		setAuthHTTPHeaders(req, server.URL, input.csrf)
	}
	req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	res, err := server.Client().Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	if res.StatusCode != input.status {
		t.Fatalf("%s %s: status %d want %d, response %s", method, input.path, res.StatusCode, input.status, data)
	}
	assertAuthNoStore(t, res)
	return string(data)
}

func TestAdminDraftPatchHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	path := draftEditorialPath + draftEditItem
	const editedHeadline, editedNote = "Edited via HTTPS", "Editor-only update"
	const patch = `{"expected_version":"1","schema_no":"1","shared":{"headline":{"kind":"string","value":"Edited via HTTPS"}}}`
	const writeOnlyPatch = `{"expected_version":"1","schema_no":"1","shared":{"note":{"kind":"string","value":"Editor-only update"}}}`
	sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path, patch, "", http.StatusForbidden})
	expireSessionStepUp(t, f.owner, f.actor.Token)

	cases := []draftHTTPRequest{
		{draftHTTPPrefix + "different/" + draftEditItem, patch, f.actor.CSRF, http.StatusNotFound},
		{draftEditorialPath + draftOtherItem, writeOnlyPatch, f.actor.CSRF, http.StatusForbidden},
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

func TestAdminDraftReadHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	path := draftEditorialPath + draftEditItem
	read := func(path string, status int) string {
		return sendDraftRequest(t, server, http.MethodGet, f.actor.Token, draftHTTPRequest{path: path, status: status})
	}
	expireSessionStepUp(t, f.owner, f.actor.Token)
	body := read(path, http.StatusOK)
	if !strings.Contains(body, initialDraftHeadline) || strings.Contains(body, initialDraftNote) {
		t.Fatalf("unexpected draft read projection: %s", body)
	}
	read(draftHTTPPrefix+"different/"+draftEditItem, http.StatusNotFound)
	read(path+"?limit=1", http.StatusBadRequest)

	_, err := f.owner.Exec(`DELETE FROM public.grant_fields WHERE grant_id IN (
SELECT id FROM public.role_grants WHERE role_id=$1 AND operation='content.items.read')`, f.roleID)
	requireNoError(t, err)
	_, err = f.owner.Exec(`DELETE FROM public.role_grants WHERE role_id=$1 AND operation='content.items.read'`, f.roleID)
	requireNoError(t, err)
	read(path, http.StatusNotFound)
	requireNoError(t, f.auth.Logout(context.Background(), f.actor, fullSessionFixtureKind))
	read(path, http.StatusUnauthorized)
	assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
}

func TestAdminDraftReadLocaleLimitHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	_, err := f.owner.Exec(`WITH languages AS (
 INSERT INTO public.languages(code,name,native_name,direction)
 SELECT 'qaa-aaa'||n,'Preview','Preview','ltr' FROM generate_series(1,16) AS n
 RETURNING code)
 INSERT INTO public.content_draft_locales(item_id,locale_code) SELECT $1,code FROM languages`, draftEditItem)
	requireNoError(t, err)
	req := draftHTTPRequest{path: draftEditorialPath + draftEditItem, status: http.StatusOK}
	sendDraftRequest(t, server, http.MethodGet, f.actor.Token, req)

	_, err = f.owner.Exec(`WITH language AS (
 INSERT INTO public.languages(code,name,native_name,direction)
 VALUES('qaa-limit','Preview','Preview','ltr') RETURNING code)
 INSERT INTO public.content_draft_locales(item_id,locale_code) SELECT $1,code FROM language`, draftEditItem)
	requireNoError(t, err)
	req.status = http.StatusServiceUnavailable
	sendDraftRequest(t, server, http.MethodGet, f.actor.Token, req)
	req.body = `{"expected_version":"1","schema_no":"1","shared":{"headline":{"kind":"string","value":"Rejected"}}}`
	req.csrf = f.actor.CSRF
	sendDraftPatch(t, server, f.actor.Token, req)
	assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
}
