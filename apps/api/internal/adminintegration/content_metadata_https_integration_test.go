package adminintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

func TestAdminDraftMetadataHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	ctx := context.Background()
	eventType := fixtureEventCollection(t, f.owner)
	server := newDraftTLSServer(t, f)
	const types = "/api/v1/admin/content-types"
	const schemaSuffix = "/schema"
	itemSchema := draftEditorialPath + draftEditItem + schemaSuffix
	get := func(path string, status int) string {
		t.Helper()
		return sendDraftRequest(t, server, http.MethodGet, f.actor.Token, draftHTTPRequest{path: path, status: status})
	}

	reviseMetadataFixture(t, f)
	expireSessionStepUp(t, f.owner, f.actor.Token)
	assertCollectionPage(t, get(types+"?limit=1", http.StatusOK), "editorial", "2", false)
	get(draftHTTPPrefix+"event/"+draftEditItem+schemaSuffix, http.StatusNotFound)
	get(itemSchema+"?limit=1", http.StatusBadRequest)

	_, err := f.owner.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES(gen_random_uuid(),$1,'content.items.read','content',$2,'assigned')`, f.roleID, eventType)
	requireNoError(t, err)
	assertCollectionPage(t, get(types+"?limit=1", http.StatusOK), "editorial", "2", true)
	assertCollectionPage(t, get(types+"?limit=1&offset=1", http.StatusOK), "event", "1", false)
	assertPinnedDraftMetadata(t, get(itemSchema, http.StatusOK))

	_, err = f.owner.Exec(`UPDATE public.role_grants SET object_scope='assigned'
 WHERE role_id=$1 AND type_id=$2 AND operation='content.items.read'`, f.roleID, draftEditorType)
	requireNoError(t, err)
	get(draftEditorialPath+draftOtherItem+schemaSuffix, http.StatusNotFound)
	requireNoError(t, disableDraftFixtureRole(ctx, f))
	get(itemSchema, http.StatusNotFound)
	page := get(types, http.StatusOK)
	if strings.TrimSpace(page) != `{"items":[],"next_offset":null}` {
		t.Fatalf("disabled role disclosed collections: %s", page)
	}
	requireNoError(t, f.auth.Logout(ctx, f.actor, fullSessionFixtureKind))
	get(types, http.StatusUnauthorized)
}

func assertCollectionPage(t *testing.T, body, key, schema string, hasNext bool) {
	t.Helper()
	var page struct {
		Items []struct {
			Key    string `json:"type_key"`
			Schema string `json:"schema_no"`
		} `json:"items"`
		Next *int `json:"next_offset"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &page))
	if len(page.Items) != 1 || page.Items[0].Key != key || page.Items[0].Schema != schema || (page.Next != nil) != hasNext {
		t.Fatalf("unexpected collection page: %s", body)
	}
}

func assertPinnedDraftMetadata(t *testing.T, body string) {
	t.Helper()
	var data struct {
		ItemID string `json:"item_id"`
		Schema string `json:"schema_no"`
		Fields []struct {
			FieldID    string                       `json:"field_id"`
			Key        string                       `json:"key"`
			Kind       string                       `json:"kind"`
			Scope      string                       `json:"scope"`
			Visibility string                       `json:"visibility"`
			Required   bool                         `json:"required"`
			Config     map[string]int               `json:"config"`
			Labels     map[string]map[string]string `json:"display_labels"`
		} `json:"fields"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	requireNoError(t, decoder.Decode(&data))
	if data.ItemID != draftEditItem || data.Schema != "1" || len(data.Fields) != 1 {
		t.Fatalf("unexpected pinned schema or field subset: %s", body)
	}
	field := data.Fields[0]
	if field.FieldID != draftHeadline || field.Key != "headline" || field.Kind != "string" || field.Scope != "shared" ||
		field.Visibility != "public" || field.Required || len(field.Config) != 0 || field.Labels["pl"]["label"] != "Zmieniony nagłówek" {
		t.Fatalf("expected old semantic constraints and current label: %s", body)
	}
}

func reviseMetadataFixture(t *testing.T, f draftEditorFixture) {
	t.Helper()
	const freshField = "b1000000-0000-4000-8000-000000000081"
	ctx := context.Background()
	requireNoError(t, identity.WithWriteMutex(ctx, f.owner, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO public.content_schemas(type_id,schema_no) SELECT id,2 FROM public.content_types WHERE id=$1`, draftEditorType)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.content_fields(type_id,field_id,key) VALUES($1,$2,'subtitle')`, draftEditorType, freshField)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,config)
 SELECT type_id,2,field_id,kind,scope,visibility,required,'{"max_length":100}'::jsonb FROM public.content_field_definitions WHERE type_id=$1 AND schema_no=1
 UNION ALL SELECT type_id,2,field_id,'string','shared','public',false,'{}'::jsonb FROM public.content_fields WHERE type_id=$1 AND field_id=$2`, draftEditorType, freshField)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `WITH revised AS (
 UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=2 RETURNING type_id)
 UPDATE public.content_types SET current_schema_no=2,version=2 WHERE id IN (SELECT type_id FROM revised)`, draftEditorType)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE public.content_fields SET display_labels='{"pl":{"label":"Zmieniony nagłówek"}}' WHERE type_id=$1 AND field_id=$2`, draftEditorType, draftHeadline)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT id,type_id,$3 FROM public.role_grants WHERE role_id=$1 AND type_id=$2 AND operation='content.items.read'`, f.roleID, draftEditorType, freshField)
		return err
	}))
}
