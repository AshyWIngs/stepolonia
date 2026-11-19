package adminintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/adminhttp"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/testpostgres"
)

const (
	publicationArticleType     = "article"
	publicationEventType       = "event"
	publicationEventsNamespace = "events"
	publicationCreateScope     = "type"
	stalePublicationCode       = "version_conflict"
	editorialSlug              = "spotkanie-w-krakowie"
	editorialTitle             = "Spotkanie w Krakowie"
	editorialNote              = "Notatka redakcyjna"
)

type publicationFixture struct {
	owner                      *sql.DB
	server                     *httptest.Server
	typeID, typeKey, namespace string
	author, publisher          identity.AuthRequest
	publisherGrant             string
}

func newPublicationFixture(t *testing.T, typeKey, namespace string) publicationFixture {
	t.Helper()
	ctx := context.Background()
	db, bootstrap, config := bootstrapFixture(t)
	requireNoError(t, content.SeedInitialRegistry(ctx, db))
	state := authFixtureFromBootstrap(t, db, bootstrap, config)
	admin := authFull(t, state)
	var typeID string
	if typeKey == publicationEventType {
		typeID = fixtureEventCollection(t, db)
	} else {
		requireNoError(t, db.QueryRow("SELECT id::text FROM public.content_types WHERE key=$1", typeKey).Scan(&typeID))
	}
	_, err := db.Exec(`INSERT INTO public.languages(code,name,native_name,direction,is_enabled)
VALUES('pl','Polish','Polski','ltr',true) ON CONFLICT(code) DO UPDATE SET is_enabled=true`)
	requireNoError(t, err)
	authorRole := fixturePublicationRole(t, db, "Pracownia tekstu")
	for _, operation := range []string{"content.items.create", "content.items.read", "content.items.update", "content.items.ready"} {
		scope := bootstrapAssigned
		if operation == "content.items.create" {
			scope = publicationCreateScope
		}
		grantPublicationFields(t, db, authorRole, typeID, operation, scope)
	}
	editorRole := fixturePublicationRole(t, db, "Dyżur wydawniczy")
	publishGrant := grantPublicationFields(t, db, editorRole, typeID, "content.items.publish", bootstrapAny)
	author := fixturePublicationAccount(t, state, admin, "writer@example.test", authorRole)
	editor := fixturePublicationAccount(t, state, admin, "publisher@example.test", editorRole)
	writerDB := openDraftEditorLogin(t, db)
	writer, err := database.NewDraftEditor(ctx, writerDB, config)
	requireNoError(t, err)
	auth, err := identity.NewAuthService(writerDB, config)
	requireNoError(t, err)
	reader := content.NewPublicReader(openPublicationRuntimeLogin(t, db))
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	handler, err := adminhttp.NewHandlerWithDraftEditor(adminhttp.Config{CanonicalOrigin: "https://" + server.Listener.Addr().String()}, auth, writer)
	requireNoError(t, err)
	server.Config.Handler = platform.HandlerWithAuth(nil, reader, slog.New(slog.NewTextHandler(io.Discard, nil)), handler)
	server.StartTLS()
	return publicationFixture{db, server, typeID, typeKey, namespace, author, editor, publishGrant}
}

func fixturePublicationRole(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var id string
	requireNoError(t, db.QueryRow(`INSERT INTO public.roles(id,name,state) VALUES(gen_random_uuid(),$1,'active') RETURNING id::text`, name).Scan(&id))
	return id
}

func grantPublicationFields(t *testing.T, db *sql.DB, role, typeID, operation, scope string) string {
	t.Helper()
	var grantID string
	requireNoError(t, db.QueryRow(`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
VALUES(gen_random_uuid(),$1,$2,'content',$3,$4) RETURNING id::text`, role, operation, typeID, scope).Scan(&grantID))
	_, err := db.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id)
SELECT $1,type_id,field_id FROM public.content_fields WHERE type_id=$2`, grantID, typeID)
	requireNoError(t, err)
	return grantID
}

func fixturePublicationAccount(t *testing.T, state authFixtureState, admin identity.SessionTokens, login, roleID string) identity.AuthRequest {
	t.Helper()
	ctx := context.Background()
	account := lifecycleEnrolled(t, state, admin, login)
	user, err := state.service.ActivateUser(ctx, authReq(admin), account.user.ID, account.user.Version)
	requireNoError(t, err)
	account.user = user
	_, err = state.service.ReplaceUserRoles(ctx, authReq(admin), user.ID, user.Version, []string{roleID})
	requireNoError(t, err)
	return authReq(lifecycleLoginFull(t, state, account))
}

func fixtureEventCollection(t *testing.T, db *sql.DB) string {
	t.Helper()
	var typeID string
	requireNoError(t, identity.WithWriteMutex(context.Background(), db, func(ctx context.Context, tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `INSERT INTO public.content_types(id,key,route_namespace)
VALUES(gen_random_uuid(),'event','events') RETURNING id::text`).Scan(&typeID)
		if err != nil {
			return err
		}
		statements := []string{
			`INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,1)`,
			`INSERT INTO public.content_fields(type_id,field_id,key)
SELECT $1::uuid,gen_random_uuid(),key FROM public.content_fields WHERE type_id=(SELECT id FROM public.content_types WHERE key='article')
UNION ALL SELECT $1::uuid,gen_random_uuid(),'editorial_note'`,
			`INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,config)
SELECT f.type_id,1,f.field_id,d.kind,d.scope,d.visibility,d.required,d.config
FROM public.content_fields f JOIN public.content_fields source USING(key)
JOIN public.content_types a ON a.id=source.type_id AND a.key='article'
JOIN public.content_field_definitions d ON d.field_id=source.field_id AND d.type_id=source.type_id AND d.schema_no=1
WHERE f.type_id=$1
UNION ALL SELECT type_id,1,field_id,'string','shared','private',false,'{}'::jsonb
FROM public.content_fields WHERE type_id=$1 AND key='editorial_note'`,
			`UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=1`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement, typeID); err != nil {
				return err
			}
		}
		return nil
	}))
	return typeID
}

func openPublicationRuntimeLogin(t *testing.T, owner *sql.DB) *sql.DB {
	t.Helper()
	cfg := testpostgres.AdminConfig(t)
	requireNoError(t, owner.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	// The cases use this login sequentially in the disposable test cluster.
	cfg.User = "sp_publication_test_reader"
	_, err := owner.Exec("CREATE ROLE sp_publication_test_reader LOGIN")
	requireNoError(t, err)
	t.Cleanup(func() {
		if _, err := owner.Exec("DROP OWNED BY sp_publication_test_reader"); err != nil {
			t.Errorf("revoke publication test reader privileges: %v", err)
		}
		if _, err := owner.Exec("DROP ROLE sp_publication_test_reader"); err != nil {
			t.Errorf("remove publication test reader: %v", err)
		}
	})
	requireNoError(t, database.GrantRuntime(context.Background(), owner, cfg.User))
	db := database.Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (f publicationFixture) prepareDraft(t *testing.T) string {
	t.Helper()
	path := draftHTTPPrefix + f.typeKey
	created := sendDraftRequest(t, f.server, http.MethodPost, f.author.Token, draftHTTPRequest{path, createDraftHTTPSBody, f.author.CSRF, http.StatusCreated})
	var receipt struct {
		ItemID string `json:"item_id"`
	}
	requireNoError(t, json.Unmarshal([]byte(created), &receipt))
	if receipt.ItemID == "" {
		t.Fatal("create response missing item ID")
	}
	shared := ""
	if f.typeKey == publicationEventType {
		shared = fmt.Sprintf(`,"shared":{"editorial_note":{"kind":"string","value":%q}}`, editorialNote)
	}
	patch := fmt.Sprintf(`{"expected_version":"1","schema_no":"1","locales":{"pl":{"values":{
"title":{"kind":"string","value":%q},"slug":{"kind":"string","value":%q},
"body":{"kind":"markdown","value":"Zapraszamy na spotkanie."}}}}%s}`, editorialTitle, editorialSlug, shared)
	sendDraftPatch(t, f.server, f.author.Token, draftHTTPRequest{path + "/" + receipt.ItemID, patch, f.author.CSRF, http.StatusOK})
	return receipt.ItemID
}

func (f publicationFixture) command(t *testing.T, actor identity.AuthRequest, item, command, body string, status int) string {
	t.Helper()
	path := draftHTTPPrefix + f.typeKey + "/" + item + "/" + command
	return sendDraftRequest(t, f.server, http.MethodPost, actor.Token, draftHTTPRequest{path, body, actor.CSRF, status})
}

func (f publicationFixture) public(t *testing.T, status int) string {
	t.Helper()
	path := "/api/v1/content/" + f.typeKey + "/" + editorialSlug + "?language=pl"
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.server.URL+path, nil)
	requireNoError(t, err)
	response, err := f.server.Client().Do(request)
	requireNoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	requireNoError(t, err)
	if response.StatusCode != status {
		t.Fatalf("public GET status %d, want %d: %s", response.StatusCode, status, body)
	}
	return string(body)
}

const (
	sharedReadyCommand = "ready-shared"
	localeReadyCommand = "ready"
	publishCommand     = "publish"
	sharedReadyBody    = `{"expected_version":"2","schema_no":"1"}`
	localeReadyBody    = `{"expected_version":"3","schema_no":"1","locale":"pl"}`
	publishBody        = `{"expected_version":"4","schema_no":"1","intended_locales":["pl"]}`
)

func TestAdminReadyPublishHTTPSIntegration(t *testing.T) {
	for _, tc := range []struct{ key, namespace string }{{publicationArticleType, "articles"}, {publicationEventType, publicationEventsNamespace}} {
		t.Run(tc.key, func(t *testing.T) {
			f := newPublicationFixture(t, tc.key, tc.namespace)
			item := f.prepareDraft(t)
			f.public(t, http.StatusNotFound)
			verifyNotReady(t, f, item)
			verifyReadyRequiresAssignment(t, f, item)
			f.command(t, f.author, item, sharedReadyCommand, sharedReadyBody, http.StatusOK)
			f.command(t, f.author, item, localeReadyCommand, localeReadyBody, http.StatusOK)
			f.public(t, http.StatusNotFound)
			verifyPublishRefusals(t, f, item)
			if tc.key == publicationArticleType {
				verifyPublicationAuditRollback(t, f, item)
			}
			response := f.command(t, f.publisher, item, publishCommand, publishBody, http.StatusOK)
			assertPublishedContent(t, f, item, response)
			if tc.key == publicationArticleType {
				verifyCurrentSchemaRequired(t, f, item)
			}
		})
	}
}

func verifyNotReady(t *testing.T, f publicationFixture, item string) {
	t.Helper()
	before := observePublication(t, f.owner, item)
	body := `{"expected_version":"2","schema_no":"1","intended_locales":["pl"]}`
	assertPublicationProblem(t, f.command(t, f.publisher, item, publishCommand, body, http.StatusConflict), "not_ready")
	f.command(t, f.author, item, publishCommand, body, http.StatusNotFound)
	assertPublicationUnchanged(t, f.owner, item, before)
}

func verifyReadyRequiresAssignment(t *testing.T, f publicationFixture, item string) {
	t.Helper()
	before := observePublication(t, f.owner, item)
	var assignee string
	requireNoError(t, f.owner.QueryRow(`DELETE FROM public.content_item_assignments WHERE item_id=$1 RETURNING user_id::text`, item).Scan(&assignee))
	f.command(t, f.author, item, sharedReadyCommand, sharedReadyBody, http.StatusNotFound)
	assertPublicationUnchanged(t, f.owner, item, before)
	_, err := f.owner.Exec(`INSERT INTO public.content_item_assignments(item_id,user_id) VALUES ($1,$2)`, item, assignee)
	requireNoError(t, err)
}

func verifyPublicationAuditRollback(t *testing.T, f publicationFixture, item string) {
	t.Helper()
	before := observePublication(t, f.owner, item)
	_, err := f.owner.Exec(`
CREATE FUNCTION public.reject_publication_success_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.operation='content.draft_published' AND NEW.result='success' AND EXISTS (
   SELECT 1 FROM public.content_items i JOIN public.content_publications p ON p.id=i.published_id
   WHERE i.id=NEW.target_id AND p.sealed
 ) THEN RAISE EXCEPTION 'test publication audit unavailable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reject_publication_success_audit BEFORE INSERT ON public.security_audit
 FOR EACH ROW EXECUTE FUNCTION public.reject_publication_success_audit()`)
	requireNoError(t, err)
	f.command(t, f.publisher, item, publishCommand, publishBody, http.StatusServiceUnavailable)
	assertPublicationUnchanged(t, f.owner, item, before)
	f.public(t, http.StatusNotFound)
	_, err = f.owner.Exec(`DROP TRIGGER reject_publication_success_audit ON public.security_audit;
DROP FUNCTION public.reject_publication_success_audit()`)
	requireNoError(t, err)
}

func verifyPublishRefusals(t *testing.T, f publicationFixture, item string) {
	t.Helper()
	before := observePublication(t, f.owner, item)
	badCSRF := f.author
	badCSRF.CSRF = strings.Repeat("A", len(badCSRF.CSRF))
	f.command(t, badCSRF, item, sharedReadyCommand, sharedReadyBody, http.StatusForbidden)
	expireSessionStepUp(t, f.owner, f.publisher.Token)
	f.command(t, f.publisher, item, publishCommand, publishBody, http.StatusForbidden)
	authRefreshStep(t, f.owner, f.publisher.Token)
	wrongType := draftHTTPPrefix + "missing/" + item + "/publish"
	sendDraftRequest(t, f.server, http.MethodPost, f.publisher.Token, draftHTTPRequest{wrongType, publishBody, f.publisher.CSRF, http.StatusNotFound})
	stale := `{"expected_version":"3","schema_no":"1"}`
	assertPublicationProblem(t, f.command(t, f.author, item, sharedReadyCommand, stale, http.StatusConflict), stalePublicationCode)
	assertPublicationUnchanged(t, f.owner, item, before)
	if f.typeKey == publicationEventType {
		verifyPrivateFieldRequired(t, f, item, before)
	}
}

func assertPublicationProblem(t *testing.T, body, want string) {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &problem))
	if problem.Code != want {
		t.Fatalf("problem code %q want %q: %s", problem.Code, want, body)
	}
}

func verifyPrivateFieldRequired(t *testing.T, f publicationFixture, item string, before publicationObservation) {
	t.Helper()
	var fieldID string
	requireNoError(t, f.owner.QueryRow(`SELECT field_id::text FROM public.content_fields WHERE type_id=$1 AND key='editorial_note'`, f.typeID).Scan(&fieldID))
	_, err := f.owner.Exec(`DELETE FROM public.grant_fields WHERE grant_id=$1 AND field_id=$2`, f.publisherGrant, fieldID)
	requireNoError(t, err)
	f.command(t, f.publisher, item, publishCommand, publishBody, http.StatusForbidden)
	assertPublicationUnchanged(t, f.owner, item, before)
	_, err = f.owner.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id) VALUES ($1,$2,$3)`, f.publisherGrant, f.typeID, fieldID)
	requireNoError(t, err)
}

func assertPublishedContent(t *testing.T, f publicationFixture, item, response string) {
	t.Helper()
	body := f.public(t, http.StatusOK)
	var public content.PublicItem
	requireNoError(t, json.Unmarshal([]byte(body), &public))
	if public.ItemID != item || public.TypeKey != f.typeKey || public.Language != "pl" || public.CanonicalURL != "/pl/"+f.namespace+"/"+editorialSlug ||
		string(public.Localized["title"].Value) != fmt.Sprintf("%q", editorialTitle) || public.PublicationID == "" {
		t.Fatalf("wrong publication read model: %s", body)
	}
	if strings.Contains(body, "editorial_note") || strings.Contains(response, editorialNote) {
		t.Fatalf("private field leaked: publication=%s, response=%s", body, response)
	}
	assertPublisherReceipt(t, response, item, public.PublicationID)
	got := observePublication(t, f.owner, item)
	if got.pointer != public.PublicationID || got.version != 5 || got.snapshots != 1 || got.routes != 1 || got.readyAudit != 2 || got.publishAudit != 1 {
		t.Fatalf("wrong persisted publication: %+v", got)
	}
}

func assertPublisherReceipt(t *testing.T, response, item, publicationID string) {
	t.Helper()
	var receipt struct {
		ItemID        string          `json:"item_id"`
		Version       string          `json:"version"`
		PublicationID string          `json:"publication_id"`
		Shared        json.RawMessage `json:"shared"`
		Locales       json.RawMessage `json:"locales"`
	}
	requireNoError(t, json.Unmarshal([]byte(response), &receipt))
	if receipt.ItemID != item || receipt.Version != "5" || receipt.PublicationID != publicationID ||
		receipt.Shared != nil || receipt.Locales != nil {
		t.Fatalf("wrong publisher receipt: %s", response)
	}
}

func verifyCurrentSchemaRequired(t *testing.T, f publicationFixture, item string) {
	t.Helper()
	before := observePublication(t, f.owner, item)
	requireNoError(t, identity.WithWriteMutex(context.Background(), f.owner, func(ctx context.Context, tx *sql.Tx) error {
		return advanceDraftCreationSchema(ctx, tx, f.typeID)
	}))
	body := `{"expected_version":"5","schema_no":"1","intended_locales":["pl"]}`
	assertPublicationProblem(t, f.command(t, f.publisher, item, publishCommand, body, http.StatusConflict), "schema_conflict")
	assertPublicationUnchanged(t, f.owner, item, before)
	f.public(t, http.StatusOK)
}

type publicationObservation struct {
	version, sharedReady                        int64
	localeReady                                 bool
	pointer                                     string
	snapshots, routes, readyAudit, publishAudit int
}

func observePublication(t *testing.T, db *sql.DB, item string) publicationObservation {
	t.Helper()
	var out publicationObservation
	requireNoError(t, db.QueryRow(`SELECT i.version,COALESCE(d.shared_ready_seq,0),COALESCE(i.published_id::text,''),
EXISTS(SELECT 1 FROM public.content_draft_locales WHERE item_id=i.id AND ready_local_seq IS NOT NULL),
(SELECT count(*) FROM public.content_publications WHERE item_id=i.id),
(SELECT count(*) FROM public.content_routes WHERE item_id=i.id),
(SELECT count(*) FROM public.security_audit WHERE operation='content.draft_ready' AND result='success' AND target_id=i.id),
(SELECT count(*) FROM public.security_audit WHERE operation='content.draft_published' AND result='success' AND target_id=i.id)
FROM public.content_items i JOIN public.content_drafts d ON d.item_id=i.id WHERE i.id=$1`, item).
		Scan(&out.version, &out.sharedReady, &out.pointer, &out.localeReady, &out.snapshots, &out.routes, &out.readyAudit, &out.publishAudit))
	return out
}

func assertPublicationUnchanged(t *testing.T, db *sql.DB, item string, want publicationObservation) {
	t.Helper()
	if got := observePublication(t, db, item); got != want {
		t.Fatalf("denied publication changed state: got %+v want %+v", got, want)
	}
}
