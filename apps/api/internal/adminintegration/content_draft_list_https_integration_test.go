package adminintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const lastListedDraft = "b1000000-0000-4000-8000-000000000006"

func TestAdminDraftListHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	const collection = draftHTTPPrefix + "editorial"
	read := func(path string, status int) string {
		t.Helper()
		return sendDraftRequest(t, server, http.MethodGet, f.actor.Token, draftHTTPRequest{path: path, status: status})
	}

	_, err := f.owner.Exec(`WITH item AS (
 INSERT INTO public.content_items(id,type_id,version) VALUES($1,$2,3) RETURNING id,type_id
 ), draft AS (
 INSERT INTO public.content_drafts(item_id,type_id,schema_no) SELECT id,type_id,1 FROM item RETURNING item_id
 )
 INSERT INTO public.content_item_assignments(item_id,user_id) SELECT item_id,$3 FROM draft`, lastListedDraft, draftEditorType, f.userID)
	requireNoError(t, err)
	expireSessionStepUp(t, f.owner, f.actor.Token)

	assertDraftListPage(t, read(collection, http.StatusOK), nil, draftEditItem, draftOtherItem, lastListedDraft)
	_, err = f.owner.Exec(`UPDATE public.role_grants SET object_scope='assigned'
 WHERE role_id=$1 AND type_id=$2 AND operation='content.items.read'`, f.roleID, draftEditorType)
	requireNoError(t, err)
	next := 1
	assertDraftListPage(t, read(collection+"?limit=1", http.StatusOK), &next, draftEditItem)
	assertDraftListPage(t, read(collection+"?limit=1&offset=1", http.StatusOK), nil, lastListedDraft)
	assertDraftListPage(t, read(collection+"?offset=2", http.StatusOK), nil)

	for _, query := range []string{"?limit=0", "?limit=101", "?offset=10001", "?offset=-1", "?limit=1&limit=2", "?sort=note", "?limit=%ZZ"} {
		read(collection+query, http.StatusBadRequest)
	}
	read(draftHTTPPrefix+"missing", http.StatusNotFound)
	_, err = f.owner.Exec(`DELETE FROM public.content_item_assignments WHERE user_id=$1`, f.userID)
	requireNoError(t, err)
	assertDraftListPage(t, read(collection, http.StatusOK), nil)
	requireNoError(t, disableDraftFixtureRole(context.Background(), f))
	read(collection, http.StatusNotFound)
	requireNoError(t, f.auth.Logout(context.Background(), f.actor, fullSessionFixtureKind))
	read(collection, http.StatusUnauthorized)
}

func assertDraftListPage(t *testing.T, body string, next *int, ids ...string) {
	t.Helper()
	type itemResponse struct {
		ItemID  string `json:"item_id"`
		TypeID  string `json:"type_id"`
		Schema  string `json:"schema_no"`
		Version string `json:"version"`
	}
	var page struct {
		Items      []itemResponse `json:"items"`
		NextOffset *int           `json:"next_offset"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	requireNoError(t, decoder.Decode(&page))
	want := make([]itemResponse, 0, len(ids))
	for _, id := range ids {
		wantVersion := "1"
		if id == lastListedDraft {
			wantVersion = "3"
		}
		want = append(want, itemResponse{ItemID: id, TypeID: draftEditorType, Schema: "1", Version: wantVersion})
	}
	if page.Items == nil || !slices.Equal(page.Items, want) || !reflect.DeepEqual(page.NextOffset, next) {
		t.Fatalf("unexpected items or next offset: %s", body)
	}
}
