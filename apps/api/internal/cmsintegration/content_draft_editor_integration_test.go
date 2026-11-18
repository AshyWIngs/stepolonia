package cmsintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/testpostgres"
)

const (
	draftEditorType  = "b1000000-0000-4000-8000-000000000001"
	draftHeadline    = "b1000000-0000-4000-8000-000000000002"
	draftNote        = "b1000000-0000-4000-8000-000000000003"
	draftEditItem    = "b1000000-0000-4000-8000-000000000004"
	draftOtherItem   = "b1000000-0000-4000-8000-000000000005"
	draftHeadlineKey = "headline"
	draftNoteKey     = "note"
)

const (
	initialDraftHeadline = "Before"
	initialDraftNote     = "private fixture value"
)

type draftEditorFixture struct {
	auth           *cms.AuthService
	editor         *content.DraftEditor
	owner          *sql.DB
	login          *sql.DB
	actor          cms.AuthRequest
	userID, roleID string
}

func newDraftEditorFixture(t *testing.T) draftEditorFixture {
	t.Helper()
	ctx := context.Background()
	f := authFixture(t)
	admin := authFull(t, f)
	actor := lifecycleEnrolled(t, f, admin, "draft-editor@example.test")
	user, err := f.service.ActivateUser(ctx, authReq(admin), actor.user.ID, actor.user.Version)
	requireNoError(t, err)
	actor.user = user
	token := lifecycleLoginFull(t, f, actor)
	roleID := createAssignedTestRole(t, f, "Draft editor", lifecycleHTTPActiveState, actor.user.ID)
	requireNoError(t, cms.WithWriteMutex(ctx, f.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO public.content_types(id,key,route_namespace) VALUES($1,'editorial','editorial')`, draftEditorType)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,1)`, draftEditorType)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.content_fields(type_id,field_id,key) VALUES($1,$2,$3),($1,$4,$5)`,
			draftEditorType, draftHeadline, draftHeadlineKey, draftNote, draftNoteKey)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required)
 VALUES($1,1,$2,'string','shared','public',false),($1,1,$3,'string','shared','private',false)`, draftEditorType, draftHeadline, draftNote)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=1`, draftEditorType)
		return err
	}))
	_, err = f.db.Exec(`INSERT INTO public.content_items(id,type_id) VALUES($1,$3),($2,$3)`, draftEditItem, draftOtherItem, draftEditorType)
	requireNoError(t, err)
	_, err = f.db.Exec(`INSERT INTO public.content_drafts(item_id,type_id,schema_no,shared_values)
 SELECT id,type_id,1,jsonb_build_object('headline',$3::text,'note',$4::text)
 FROM public.content_items WHERE id IN ($1,$2)`, draftEditItem, draftOtherItem, initialDraftHeadline, initialDraftNote)
	requireNoError(t, err)
	_, err = f.db.Exec(`INSERT INTO public.content_item_assignments(user_id,item_id) VALUES($2,$1)`, draftEditItem, actor.user.ID)
	requireNoError(t, err)
	_, err = f.db.Exec(`WITH grants AS (
 INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES
 (gen_random_uuid(),$1,'content.items.update','content',$2,'assigned'),
 (gen_random_uuid(),$1,'content.items.update','content',$2,'any'),
 (gen_random_uuid(),$1,'content.items.read','content',$2,'any')
 RETURNING id,type_id,object_scope
 ) INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT id,type_id,CASE WHEN object_scope='assigned' THEN $4::uuid ELSE $3::uuid END
 FROM grants`, roleID, draftEditorType, draftHeadline, draftNote)
	requireNoError(t, err)
	db := openDraftEditorLogin(t, f.db)
	editor, err := database.NewDraftEditor(ctx, db, f.config)
	requireNoError(t, err)
	if err := database.CheckSecurity(ctx, db); err == nil {
		t.Fatal("combined login accepted as authentication-only pool")
	}
	if _, err := database.NewDraftEditor(ctx, f.db, f.config); err == nil {
		t.Fatal("privileged login accepted as Engine pool")
	}
	auth, err := cms.NewAuthService(db, f.config)
	requireNoError(t, err)
	return draftEditorFixture{auth, editor, f.db, db, authReq(token), actor.user.ID, roleID}
}

func openDraftEditorLogin(t *testing.T, owner *sql.DB) *sql.DB {
	t.Helper()
	cfg := testpostgres.CMSConfig(t)
	requireNoError(t, owner.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_editor"
	role := pgx.Identifier{cfg.User}.Sanitize()
	_, err := owner.Exec("CREATE ROLE " + role + " LOGIN IN ROLE sp_publisher, sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = owner.Exec("DROP ROLE " + role) })
	_, err = owner.Exec("REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " FROM PUBLIC")
	requireNoError(t, err)
	db := database.Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func editDraftField(key, value string) content.DraftPatch {
	return content.DraftPatch{SchemaNo: 1, Shared: map[string]content.FieldValue{
		key: {Kind: "string", Value: json.RawMessage(value)},
	}}
}

func TestCMSDraftEditorFieldAuthorizationIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	ctx := context.Background()
	req := f.actor
	req.CSRF = ""
	out, err := f.editor.SaveDraft(ctx, req, draftEditItem, 1, editDraftField(draftHeadlineKey, `"Without CSRF"`))
	onboardingError(t, err, cms.ErrCSRF)
	if out.ItemID != "" {
		t.Fatal("denial returned a draft")
	}
	expireSessionStepUp(t, f.owner, f.actor.Token)
	_, err = f.editor.SaveDraft(ctx, f.actor, draftEditItem, 1, editDraftField(draftHeadlineKey, `"Stale MFA"`))
	onboardingError(t, err, cms.ErrStepUpRequired)
	authRefreshStep(t, f.owner, f.actor.Token)

	_, err = f.editor.SaveDraft(ctx, f.actor, draftOtherItem, 999, editDraftField(draftNoteKey, `"Without assignment"`))
	onboardingError(t, err, cms.ErrPermissionDenied)
	_, err = f.editor.SaveDraft(ctx, f.actor, draftEditItem, 1, editDraftField("unknown", `"Unknown field"`))
	onboardingError(t, err, cms.ErrPermissionDenied)
	patch := editDraftField(draftHeadlineKey, `"After"`)
	patch.Shared[draftNoteKey] = content.FieldValue{Kind: "string", Value: json.RawMessage(`"Stored but hidden"`)}
	out, err = f.editor.SaveDraft(ctx, f.actor, draftEditItem, 1, patch)
	requireNoError(t, err)
	if out.Version != 2 || len(out.Shared) != 1 || string(out.Shared[draftHeadlineKey]) != `"After"` {
		t.Fatalf("unexpected projected response: %+v", out)
	}
	assertDraftEditState(t, f.owner, draftEditItem, 2, "After", "Stored but hidden")
	assertDraftEditState(t, f.owner, draftOtherItem, 1, initialDraftHeadline, initialDraftNote)
	_, err = f.editor.SaveDraft(ctx, f.actor, draftEditItem, 1, patch)
	var conflict *content.Error
	if !errors.As(err, &conflict) || conflict.Kind != content.Conflict {
		t.Fatalf("expected optimistic conflict, got %v", err)
	}
	var auditCount int
	requireNoError(t, f.owner.QueryRow(`SELECT count(*) FROM public.security_audit
 WHERE operation='content.draft_saved' AND result='success' AND actor_id=$1 AND target_id=$2`, f.userID, draftEditItem).Scan(&auditCount))
	if auditCount != 1 {
		t.Fatalf("expected one successful audit, got %d", auditCount)
	}
	_, err = f.login.Exec(`DELETE FROM public.security_audit`)
	assertSQLState(t, err, "42501")
}

func TestCMSDraftEditorBlindWriteIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	ctx := context.Background()
	visible := editDraftField(draftHeadlineKey, `"Before"`)
	out, err := f.editor.SaveDraft(ctx, f.actor, draftEditItem, 1, visible)
	requireNoError(t, err)
	if out.Version != 1 {
		t.Fatalf("readable no-op advanced version to %d", out.Version)
	}
	blind := editDraftField(draftNoteKey, `"private fixture value"`)
	unset := content.DraftPatch{SchemaNo: 1, UnsetShared: []string{draftNoteKey}}
	for i, patch := range []content.DraftPatch{blind, unset, unset} {
		version := int64(i + 1)
		out, err := f.editor.SaveDraft(ctx, f.actor, draftEditItem, version, patch)
		requireNoError(t, err)
		if out.Version != version+1 || out.SharedSeq != version+1 || len(out.Shared) != 1 {
			t.Fatalf("blind write did not advance version and sequence while hiding the field: %+v", out)
		}
	}
}

func TestCMSDraftEditorAuditRollbackIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	_, err := f.owner.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT test_deny_draft_audit CHECK (operation<>'content.draft_saved')`)
	requireNoError(t, err)
	out, err := f.editor.SaveDraft(context.Background(), f.actor, draftEditItem, 1, editDraftField(draftNoteKey, `"Rollback"`))
	onboardingError(t, err, cms.ErrSecurityUnavailable)
	if out.ItemID != "" {
		t.Fatal("rolled-back draft returned a response")
	}
	assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
}

type draftRevocation struct {
	name   string
	revoke func(context.Context, draftEditorFixture) error
	want   error
}

func TestCMSDraftEditorRevocationIntegration(t *testing.T) {
	cases := []draftRevocation{
		{"session", func(ctx context.Context, f draftEditorFixture) error {
			return f.auth.Logout(ctx, f.actor, fullSessionFixtureKind)
		}, cms.ErrAuthenticationRequired},
		{"role", func(ctx context.Context, f draftEditorFixture) error {
			return revokeDraftAccess(ctx, f.owner, `UPDATE public.roles SET state='disabled' WHERE id=$1`, f.roleID)
		}, cms.ErrNotFound},
		{"assignment", func(ctx context.Context, f draftEditorFixture) error {
			return revokeDraftAccess(ctx, f.owner, `DELETE FROM public.content_item_assignments WHERE user_id=$1`, f.userID)
		}, cms.ErrPermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/edit-first", func(t *testing.T) { checkDraftRevocationOrder(t, tc, true) })
		t.Run(tc.name+"/revoke-first", func(t *testing.T) { checkDraftRevocationOrder(t, tc, false) })
	}
}

func revokeDraftAccess(ctx context.Context, db *sql.DB, statement, id string) error {
	return cms.WithWriteMutex(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, statement, id)
		return err
	})
}

func checkDraftRevocationOrder(t *testing.T, tc draftRevocation, editFirst bool) {
	t.Helper()
	f := newDraftEditorFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	edit := func() error {
		_, err := f.editor.SaveDraft(ctx, f.actor, draftEditItem, 1, editDraftField(draftNoteKey, `"Edited before revocation"`))
		return err
	}
	revoke := func() error { return tc.revoke(ctx, f) }
	first, second := revoke, edit
	if editFirst {
		first, second = edit, revoke
	}
	blocker := holdInstallationWriteMutex(t, f.owner)
	firstResult, secondResult := make(chan error, 1), make(chan error, 1)
	go func() { firstResult <- first() }()
	waitForInstallationWriteWaiters(t, f.owner, 1)
	go func() { secondResult <- second() }()
	waitForInstallationWriteWaiters(t, f.owner, 2)
	requireNoError(t, blocker.Commit())
	requireNoError(t, <-firstResult)
	if editFirst {
		requireNoError(t, <-secondResult)
		assertDraftEditState(t, f.owner, draftEditItem, 2, initialDraftHeadline, "Edited before revocation")
	} else {
		onboardingError(t, <-secondResult, tc.want)
		assertDraftEditState(t, f.owner, draftEditItem, 1, initialDraftHeadline, initialDraftNote)
	}
}

func assertDraftEditState(t *testing.T, db *sql.DB, item string, version int64, headline, note string) {
	t.Helper()
	var actualVersion int64
	var actualHeadline, actualNote string
	requireNoError(t, db.QueryRow(`SELECT i.version,d.shared_values->>'headline',d.shared_values->>'note'
 FROM public.content_items i JOIN public.content_drafts d ON d.item_id=i.id WHERE i.id=$1`, item).Scan(&actualVersion, &actualHeadline, &actualNote))
	if actualVersion != version || actualHeadline != headline || actualNote != note {
		t.Fatalf("unexpected saved draft %s at version %d", item, actualVersion)
	}
}
