package adminintegration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminDraftLocaleWriteLimitHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	_, err := f.owner.Exec(`WITH languages AS (
 INSERT INTO public.languages(code,name,native_name,direction,is_enabled)
 SELECT 'qaa-x-draft'||n,'Preview','Preview','ltr',n>15 FROM generate_series(1,17) AS n RETURNING code,is_enabled
 )
 INSERT INTO public.content_draft_locales(item_id,locale_code) SELECT $1,code FROM languages WHERE NOT is_enabled`, draftEditItem)
	requireNoError(t, err)
	const path = draftEditorialPath + draftEditItem
	const addSixteenth = `{"expected_version":"1","schema_no":"1","locales":{"qaa-x-draft16":{}}}`
	saved := sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path: path, body: addSixteenth, csrf: f.actor.CSRF, status: http.StatusOK})
	if !strings.Contains(saved, `"qaa-x-draft16"`) {
		t.Fatalf("sixteenth locale missing from the saved projection: %s", saved)
	}
	const addSeventeenth = `{"expected_version":"2","schema_no":"1","shared":{"headline":{"kind":"string","value":"Must roll back"}},"locales":{"qaa-x-draft17":{}}}`
	sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path: path, body: addSeventeenth, csrf: f.actor.CSRF, status: http.StatusBadRequest})
	assertDraftEditState(t, f.owner, draftEditItem, 2, initialDraftHeadline, initialDraftNote)
	var count int
	requireNoError(t, f.owner.QueryRow(`SELECT count(*) FROM public.content_draft_locales WHERE item_id=$1`, draftEditItem).Scan(&count))
	if count != 16 {
		t.Fatalf("locale count after rejected write: %d", count)
	}
}

func TestAdminDraftAdoptSchemaHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	reviseMetadataFixture(t, f)
	expireSessionStepUp(t, f.owner, f.actor.Token)
	_, err := f.owner.Exec(`UPDATE public.content_drafts SET shared_ready_seq=shared_seq WHERE item_id=$1`, draftEditItem)
	requireNoError(t, err)
	const path = draftEditorialPath + draftEditItem + "/adopt-schema"
	const adopt = `{"expected_version":"1","schema_no":"2"}`
	post := func(body string, status int) string {
		t.Helper()
		return sendDraftRequest(t, server, http.MethodPost, f.actor.Token, draftHTTPRequest{path: path, body: body, csrf: f.actor.CSRF, status: status})
	}
	post(adopt, http.StatusForbidden)
	_, err = f.owner.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT g.id,g.type_id,f.field_id FROM public.role_grants g JOIN public.content_fields f ON f.type_id=g.type_id
 WHERE g.role_id=$1 AND g.type_id=$2 AND g.operation='content.items.update' AND g.object_scope='any'
 ON CONFLICT DO NOTHING`, f.roleID, draftEditorType)
	requireNoError(t, err)
	assertPublicationProblem(t, post(`{"expected_version":"1","schema_no":"1"}`, http.StatusConflict), "schema_conflict")
	response := post(adopt, http.StatusOK)
	if !strings.Contains(response, `"schema_no":"2"`) || !strings.Contains(response, `"shared_ready_seq":null`) ||
		!strings.Contains(response, initialDraftHeadline) || strings.Contains(response, initialDraftNote) {
		t.Fatalf("unexpected adoption response: %s", response)
	}
	assertDraftEditState(t, f.owner, draftEditItem, 2, initialDraftHeadline, initialDraftNote)
	var adopted int
	requireNoError(t, f.owner.QueryRow(`SELECT count(*) FROM public.security_audit
 WHERE target_id=$1 AND operation='content.draft_schema_adopted' AND result='success'`, draftEditItem).Scan(&adopted))
	if adopted != 1 {
		t.Fatalf("schema adoption success audit count: %d", adopted)
	}
	assertWriteOnlyAdoption(t, f, server, adopt)
}

func assertWriteOnlyAdoption(t *testing.T, f draftEditorFixture, server *httptest.Server, body string) {
	t.Helper()
	_, err := f.owner.Exec(`DELETE FROM public.grant_fields WHERE grant_id IN (
 SELECT id FROM public.role_grants WHERE role_id=$1 AND operation='content.items.read')`, f.roleID)
	requireNoError(t, err)
	_, err = f.owner.Exec(`DELETE FROM public.role_grants WHERE role_id=$1 AND operation='content.items.read'`, f.roleID)
	requireNoError(t, err)
	path := draftEditorialPath + draftOtherItem + "/adopt-schema"
	response := sendDraftRequest(t, server, http.MethodPost, f.actor.Token, draftHTTPRequest{path, body, f.actor.CSRF, http.StatusOK})
	if !strings.Contains(response, `"schema_no":"2"`) || !strings.Contains(response, `"version":"2"`) ||
		strings.Contains(response, `"shared"`) || strings.Contains(response, `"locales"`) {
		t.Fatalf("unexpected write-only schema receipt: %s", response)
	}
}

func TestAdminDraftUnpublishHTTPSIntegration(t *testing.T) {
	f := newPublicationFixture(t, publicationEventType, publicationEventsNamespace)
	item := f.prepareDraft(t)
	f.command(t, f.author, item, sharedReadyCommand, sharedReadyBody, http.StatusOK)
	f.command(t, f.author, item, localeReadyCommand, localeReadyBody, http.StatusOK)
	f.command(t, f.publisher, item, publishCommand, publishBody, http.StatusOK)
	f.public(t, http.StatusOK)
	const command = "unpublish"
	const request = `{"expected_version":"5","schema_no":"1"}`
	before := observePublication(t, f.owner, item)
	f.command(t, f.publisher, item, command, request, http.StatusNotFound)
	assertPublicationUnchanged(t, f.owner, item, before)
	_, err := f.owner.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 SELECT gen_random_uuid(),role_id,'content.items.unpublish','content',type_id,'any'
 FROM public.role_grants WHERE id=$1`, f.publisherGrant)
	requireNoError(t, err)
	expireSessionStepUp(t, f.owner, f.publisher.Token)
	f.command(t, f.publisher, item, command, publishBody, http.StatusBadRequest)
	f.command(t, f.publisher, item, command, request, http.StatusOK)
	f.public(t, http.StatusNotFound)
	after := observePublication(t, f.owner, item)
	if after.pointer != "" || after.version != before.version+1 || after.snapshots != before.snapshots || after.routes != before.routes {
		t.Fatalf("unpublish changed history or failed to clear the pointer: before=%+v after=%+v", before, after)
	}
	var count int
	requireNoError(t, f.owner.QueryRow(`SELECT count(*) FROM public.content_draft_locales WHERE item_id=$1 AND ready_local_seq IS NOT NULL`, item).Scan(&count))
	if count != 0 {
		t.Fatal("unpublish did not clear locale readiness")
	}
}
