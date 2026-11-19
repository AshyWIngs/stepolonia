package adminintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
)

const (
	createDraftHTTPSPath   = draftHTTPPrefix + "article"
	createDraftHTTPSBody   = `{"schema_no":"1"}`
	createDraftCurrentBody = `{"schema_no":"2"}`
)

func grantDraftCreate(t *testing.T, f draftEditorFixture, typeID string) {
	t.Helper()
	_, err := f.owner.Exec(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
VALUES(gen_random_uuid(),$1,'content.items.create','content',$2,'type')`, f.roleID, typeID)
	requireNoError(t, err)
}

func newArticleAuthorFixture(t *testing.T) (draftEditorFixture, string) {
	t.Helper()
	ctx := context.Background()
	db, bootstrap, config := bootstrapFixture(t)
	requireNoError(t, content.SeedInitialRegistry(ctx, db))
	state := authFixtureFromBootstrap(t, db, bootstrap, config)
	admin := authFull(t, state)
	author := lifecycleEnrolled(t, state, admin, "article-author@example.test")
	user, err := state.service.ActivateUser(ctx, authReq(admin), author.user.ID, author.user.Version)
	requireNoError(t, err)
	author.user = user
	var authorRoleID, articleTypeID string
	requireNoError(t, db.QueryRow(`SELECT id::text FROM public.roles WHERE name='Author'`).Scan(&authorRoleID))
	requireNoError(t, db.QueryRow(`SELECT id::text FROM public.content_types WHERE key='article'`).Scan(&articleTypeID))
	_, err = state.service.ReplaceUserRoles(ctx, authReq(admin), user.ID, user.Version, []string{authorRoleID})
	requireNoError(t, err)
	_, err = db.Exec(`INSERT INTO public.languages(code,name,native_name,direction,is_enabled)
VALUES('pl','Polish','Polski','ltr',true) ON CONFLICT(code) DO UPDATE SET is_enabled=true`)
	requireNoError(t, err)
	login := openDraftEditorLogin(t, db)
	editor, err := database.NewDraftEditor(ctx, login, config)
	requireNoError(t, err)
	auth, err := identity.NewAuthService(login, config)
	requireNoError(t, err)
	return draftEditorFixture{
		auth: auth, editor: editor, owner: db, login: login,
		actor: authReq(lifecycleLoginFull(t, state, author)), userID: user.ID, roleID: authorRoleID,
	}, articleTypeID
}

func TestAdminDraftCreateHTTPSIntegration(t *testing.T) {
	f, articleTypeID := newArticleAuthorFixture(t)
	server := newDraftTLSServer(t, f)
	post := func(body, csrf string, status int) string {
		return sendDraftRequest(t, server, http.MethodPost, f.actor.Token, draftHTTPRequest{createDraftHTTPSPath, body, csrf, status})
	}
	before := snapshotDraftCreate(t, f.owner)
	requireNoError(t, identity.WithWriteMutex(context.Background(), f.owner, func(ctx context.Context, tx *sql.Tx) error {
		return advanceDraftCreationSchema(ctx, tx, articleTypeID)
	}))
	forgedCSRF := strings.Repeat("A", len(f.actor.CSRF))
	post(createDraftCurrentBody, forgedCSRF, http.StatusForbidden)
	expireSessionStepUp(t, f.owner, f.actor.Token)
	stale := post(createDraftHTTPSBody, f.actor.CSRF, http.StatusConflict)
	if !strings.Contains(stale, `"schema_conflict"`) {
		t.Fatalf("expected schema conflict, got %s", stale)
	}
	assertDraftCreateSnapshot(t, f.owner, before)

	body := post(createDraftCurrentBody, f.actor.CSRF, http.StatusCreated)
	itemID := assertCreatedArticleDraft(t, f, body, articleTypeID, before)
	assertArticleDraftCanBeSaved(t, server, f.actor, itemID)

	before = snapshotDraftCreate(t, f.owner)
	rejectCreatedDraftAudit(t, f.owner)
	post(createDraftCurrentBody, f.actor.CSRF, http.StatusServiceUnavailable)
	assertDraftCreateSnapshot(t, f.owner, before)
}

func assertCreatedArticleDraft(t *testing.T, f draftEditorFixture, body, articleTypeID string, before draftCreateSnapshot) string {
	t.Helper()
	var created struct {
		ItemID  string `json:"item_id"`
		TypeID  string `json:"type_id"`
		Schema  string `json:"schema_no"`
		Version string `json:"version"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &created))
	if created.ItemID == "" || created.TypeID != articleTypeID || created.Schema != "2" || created.Version != "1" {
		t.Fatalf("unexpected create receipt: %s", body)
	}
	assertDraftCreateSnapshot(t, f.owner, draftCreateSnapshot{before.items + 1, before.drafts + 1, before.assignments + 1, before.audit + 1})
	var assignmentAndAudit bool
	requireNoError(t, f.owner.QueryRow(`SELECT
EXISTS(SELECT 1 FROM public.content_item_assignments WHERE item_id=$1 AND user_id=$2)
AND EXISTS(SELECT 1 FROM public.security_audit WHERE operation='content.draft_created'
 AND target_id=$1 AND actor_id=$2 AND request_id IS NOT NULL AND result='success')`, created.ItemID, f.userID).Scan(&assignmentAndAudit))
	if !assignmentAndAudit {
		t.Fatal("create did not assign the author and audit the resulting item")
	}

	return created.ItemID
}

func assertArticleDraftCanBeSaved(t *testing.T, server *httptest.Server, actor identity.AuthRequest, itemID string) {
	t.Helper()
	path := createDraftHTTPSPath + "/" + itemID
	initial := sendDraftRequest(t, server, http.MethodGet, actor.Token, draftHTTPRequest{path: path, status: http.StatusOK})
	assertArticleDraftTitle(t, initial, "1", "")
	const title = "Nowy artykuł"
	const patch = `{"expected_version":"1","schema_no":"2","locales":{"pl":{"values":{"title":{"kind":"string","value":"` + title + `"}}}}}`
	saved := sendDraftPatch(t, server, actor.Token, draftHTTPRequest{path, patch, actor.CSRF, http.StatusOK})
	assertArticleDraftTitle(t, saved, "2", title)
	reloaded := sendDraftRequest(t, server, http.MethodGet, actor.Token, draftHTTPRequest{path: path, status: http.StatusOK})
	assertArticleDraftTitle(t, reloaded, "2", title)
}

func assertArticleDraftTitle(t *testing.T, body, version, title string) {
	t.Helper()
	var draft struct {
		Version string                     `json:"version"`
		Shared  map[string]json.RawMessage `json:"shared"`
		Locales map[string]struct {
			Values map[string]string `json:"values"`
		} `json:"locales"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &draft))
	if draft.Version != version || draft.Shared == nil || len(draft.Shared) != 0 || draft.Locales == nil ||
		draft.Locales["pl"].Values["title"] != title || title == "" && len(draft.Locales) != 0 {
		t.Fatalf("expected article draft version %s with Polish title %q: %s", version, title, body)
	}
}

func rejectCreatedDraftAudit(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
CREATE FUNCTION public.reject_draft_create_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.operation='content.draft_created' AND NEW.result='success' AND EXISTS (
  SELECT 1 FROM public.content_drafts d
  JOIN public.content_item_assignments a USING(item_id)
  WHERE d.item_id=NEW.target_id AND a.user_id=NEW.actor_id
 ) THEN RAISE EXCEPTION 'test create audit unavailable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reject_draft_create_audit BEFORE INSERT ON public.security_audit
 FOR EACH ROW EXECUTE FUNCTION public.reject_draft_create_audit()
`)
	requireNoError(t, err)
}

func advanceDraftCreationSchema(ctx context.Context, tx *sql.Tx, typeID string) error {
	statements := []string{
		`INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,2)`,
		`INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,target_type_id,config)
SELECT type_id,2,field_id,kind,scope,visibility,required,target_type_id,config
FROM public.content_field_definitions WHERE type_id=$1 AND schema_no=1`,
		`UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=2`,
		`UPDATE public.content_types SET current_schema_no=2,version=2 WHERE id=$1`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, typeID); err != nil {
			return err
		}
	}
	return nil
}

func TestAdminDraftCreateWaitsForRevocationHTTPSIntegration(t *testing.T) {
	f := newDraftEditorFixture(t)
	server := newDraftTLSServer(t, f)
	before := snapshotDraftCreate(t, f.owner)
	const path = draftHTTPPrefix + "editorial"
	denied := sendDraftRequest(t, server, http.MethodPost, f.actor.Token, draftHTTPRequest{path, createDraftHTTPSBody, f.actor.CSRF, http.StatusNotFound})
	missing := sendDraftRequest(t, server, http.MethodPost, f.actor.Token, draftHTTPRequest{draftHTTPPrefix + "missing", createDraftHTTPSBody, f.actor.CSRF, http.StatusNotFound})
	if denied != missing {
		t.Fatalf("different responses for forbidden and missing type: %s versus %s", denied, missing)
	}
	assertDraftCreateSnapshot(t, f.owner, before)
	grantDraftCreate(t, f, draftEditorType)
	lock := holdInstallationWriteMutex(t, f.owner)
	_, err := lock.Exec(`DELETE FROM public.role_grants WHERE role_id=$1 AND operation='content.items.create'`, f.roleID)
	requireNoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, strings.NewReader(createDraftHTTPSBody))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, server.URL, f.actor.CSRF)
	req.AddCookie(&http.Cookie{Name: authFullSessionCookie, Value: f.actor.Token, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	type reply struct {
		response *http.Response
		err      error
	}
	result := make(chan reply, 1)
	go func() {
		response, err := server.Client().Do(req)
		result <- reply{response, err}
	}()
	waitForInstallationWriteWaiters(t, f.owner, 1)
	requireNoError(t, lock.Commit())
	got := <-result
	requireNoError(t, got.err)
	defer func() { _ = got.response.Body.Close() }()
	if got.response.StatusCode != http.StatusNotFound {
		body, err := io.ReadAll(got.response.Body)
		requireNoError(t, err)
		t.Fatalf("queued POST after create revocation: status %d, response %s", got.response.StatusCode, body)
	}
	assertDraftCreateSnapshot(t, f.owner, before)
}

type draftCreateSnapshot struct {
	items, drafts, assignments, audit int
}

func snapshotDraftCreate(t *testing.T, db *sql.DB) draftCreateSnapshot {
	t.Helper()
	var out draftCreateSnapshot
	requireNoError(t, db.QueryRow(`SELECT
(SELECT count(*) FROM public.content_items),
(SELECT count(*) FROM public.content_drafts),
(SELECT count(*) FROM public.content_item_assignments),
(SELECT count(*) FROM public.security_audit WHERE operation='content.draft_created' AND result='success')`).
		Scan(&out.items, &out.drafts, &out.assignments, &out.audit))
	return out
}

func assertDraftCreateSnapshot(t *testing.T, db *sql.DB, want draftCreateSnapshot) {
	t.Helper()
	if got := snapshotDraftCreate(t, db); got != want {
		t.Fatalf("create side effects: got %+v want %+v", got, want)
	}
}
