package adminintegration

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/identity"
)

const linkedDraftField = "b1000000-0000-4000-8000-000000000082"

func TestAdminDraftRelationChangeHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	installDraftRelationFixture(t, f)
	const path = draftEditorialPath + draftEditItem
	post := func(body string, status int) string {
		t.Helper()
		return sendDraftPatch(t, server, f.actor.Token, draftHTTPRequest{path: path, body: body, csrf: f.actor.CSRF, status: status})
	}
	relation := fmt.Sprintf(`{"field_id":%q,"target_item_id":%q}`, linkedDraftField, draftOtherItem)
	set := func(version int, changes string) string {
		return fmt.Sprintf(`{"expected_version":"%d","schema_no":"2","relation_changes":[%s]}`, version, changes)
	}
	post(set(1, relation), http.StatusForbidden)
	assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
	_, err := f.owner.Exec(`INSERT INTO public.content_item_assignments(user_id,item_id) VALUES($1,$2)`, f.userID, draftOtherItem)
	requireNoError(t, err)
	post(set(1, relation+","+relation), http.StatusBadRequest)
	assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
	response := post(set(1, relation), http.StatusOK)
	if !strings.Contains(response, draftOtherItem) || !strings.Contains(response, `"shared_ready_seq":null`) {
		t.Fatalf("written relation or readiness reset missing: %s", response)
	}
	assertDraftEditState(t, f.owner, draftEditItem, 2, initialDraftHeadline, initialDraftNote)

	// A loss of read access to the old target does not prevent explicitly
	// clearing the slot or reveal whether it previously held a target.
	_, err = f.owner.Exec(`DELETE FROM public.content_item_assignments WHERE item_id=$1 AND user_id=$2`, draftOtherItem, f.userID)
	requireNoError(t, err)
	clear := fmt.Sprintf(`{"field_id":%q}`, linkedDraftField)
	post(set(2, clear), http.StatusOK)
	post(set(3, clear), http.StatusOK)
	assertDraftEditState(t, f.owner, draftEditItem, 4, initialDraftHeadline, initialDraftNote)
	var count int
	requireNoError(t, f.owner.QueryRow(`SELECT count(*) FROM public.content_draft_relations WHERE item_id=$1`, draftEditItem).Scan(&count))
	if count != 0 {
		t.Fatalf("relations remain after clearing: %d", count)
	}
}

func installDraftRelationFixture(t *testing.T, f draftEditorFixture) {
	t.Helper()
	requireNoError(t, identity.WithWriteMutex(context.Background(), f.owner, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `WITH schema AS (
 INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,2) RETURNING type_id,schema_no
 ), field AS (
 INSERT INTO public.content_fields(type_id,field_id,key) SELECT type_id,$2::uuid,'related_draft' FROM schema RETURNING type_id,field_id
 )
 INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,target_type_id)
 SELECT type_id,2,field_id,'relation','shared','public',false,type_id FROM field`, draftEditorType, linkedDraftField); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,config)
 SELECT type_id,2,field_id,kind,scope,visibility,required,config FROM public.content_field_definitions WHERE type_id=$1 AND schema_no=1`, draftEditorType); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `WITH sealed AS (
 UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=2 RETURNING type_id,schema_no
 )
 UPDATE public.content_drafts d SET schema_no=s.schema_no,shared_ready_seq=d.shared_seq
 FROM sealed s WHERE d.type_id=s.type_id AND d.item_id=$2`, draftEditorType, draftEditItem); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `WITH read_grant AS (
 UPDATE public.role_grants SET object_scope='assigned' WHERE role_id=$1 AND type_id=$2 AND operation='content.items.read'
 RETURNING id,type_id
 )
 INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT id,type_id,$3::uuid FROM read_grant
 UNION ALL SELECT id,type_id,$3::uuid FROM public.role_grants WHERE role_id=$1 AND type_id=$2 AND operation='content.items.update' AND object_scope='assigned'`, f.roleID, draftEditorType, linkedDraftField)
		return err
	}))
}
