package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/publishing"
)

type applicationFixture struct {
	ctx     context.Context
	owner   *sql.DB
	editor  *publishing.Service
	reader  *publishing.PublicReader
	cfg     *pgx.ConnConfig
	runtime *sql.DB
}

func testPublishingApplication(t *testing.T, ctx context.Context, owner *sql.DB, cfg *pgx.ConnConfig) {
	_, err := owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code IN ('ru','pl','de')")
	requireNoError(t, err)
	editorDB := applicationDatabase(t, cfg, "sp_publisher", "publishing-editor")
	runtimeCfg := cfg.Copy()
	runtimeCfg.User = "stage12_app"
	runtimeCfg.Password = readDisposablePassword(t, os.Getenv("STAGE12_TEST_RUNTIME_PASSWORD_FILE"))
	runtime := applicationDatabase(t, runtimeCfg, "", "publishing-runtime")
	f := applicationFixture{ctx, owner, publishing.NewService(editorDB), publishing.NewPublicReader(runtime), cfg, runtime}
	t.Run("application partial patches ready and privilege boundaries", f.testDraft)
	t.Run("application publication merges languages and resolves aliases", f.testPublication)
	t.Run("application failed publication is atomic", f.testRollback)
	t.Run("application unpublish resets the next publication", f.testUnpublish)
	t.Run("application concurrent expected versions", f.testVersions)
	t.Run("application concurrent URL reservation", f.testRoutes)
	t.Run("application publication and language disable", f.testDisable)
}

func applicationDatabase(t *testing.T, cfg *pgx.ConnConfig, role, name string) *sql.DB {
	t.Helper()
	clone := cfg.Copy()
	clone.RuntimeParams["application_name"] = name
	if role != "" {
		clone.RuntimeParams["role"] = role
	}
	db := Open(clone)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func valueField(value string) publishing.FieldPatch {
	return publishing.FieldPatch{Set: true, Value: &value}
}

func completeTranslation(slug string) publishing.TranslationPatch {
	return publishing.TranslationPatch{Title: valueField("Title " + slug), Slug: valueField(slug), BodyMarkdown: valueField("Body " + slug)}
}

func createApplicationDraft(t *testing.T, f applicationFixture, patch map[string]publishing.TranslationPatch) publishing.ArticleState {
	t.Helper()
	state, err := f.editor.CreateDraft(f.ctx, patch)
	requireNoError(t, err)
	return state
}

func readyApplicationDraft(t *testing.T, f applicationFixture, state publishing.ArticleState, codes ...string) publishing.ArticleState {
	t.Helper()
	sort.Strings(codes)
	for _, code := range codes {
		next, err := f.editor.MarkTranslationReady(f.ctx, state.ID, code, state.Version)
		requireNoError(t, err)
		state = next
	}
	return state
}

func publishApplicationDraft(t *testing.T, f applicationFixture, patch map[string]publishing.TranslationPatch) publishing.ArticleState {
	t.Helper()
	state := createApplicationDraft(t, f, patch)
	var codes []string
	for code := range patch {
		codes = append(codes, code)
	}
	state = readyApplicationDraft(t, f, state, codes...)
	next, err := f.editor.Publish(f.ctx, state.ID, state.Version)
	requireNoError(t, err)
	return next
}

func assertPublishingError(t *testing.T, err error, kind publishing.Kind) *publishing.Error {
	t.Helper()
	var problem *publishing.Error
	if !errors.As(err, &problem) || problem.Kind != kind {
		t.Fatalf("expected %s; got %v", kind, err)
	}
	return problem
}

// Снимок сверяет реальные следы отката, включая строки, которые API не возвращает.
func applicationSnapshot(t *testing.T, f applicationFixture, id string) string {
	t.Helper()
	const query = `SELECT json_build_object(
 'article',(SELECT row_to_json(a) FROM public.articles a WHERE id=$1),
 'drafts',(SELECT json_agg(d ORDER BY language_code) FROM public.article_draft_translations d WHERE article_id=$1),
 'revisions',(SELECT json_agg(r ORDER BY revision_no) FROM public.article_revisions r WHERE article_id=$1),
 'translations',(SELECT json_agg(t ORDER BY revision_id,language_code) FROM public.article_revision_translations t WHERE article_id=$1),
 'routes',(SELECT json_agg(p ORDER BY language_code,slug) FROM public.public_routes p WHERE article_id=$1))::text`
	var state string
	requireNoError(t, f.owner.QueryRowContext(f.ctx, query, id).Scan(&state))
	return state
}

func (f applicationFixture) testDraft(t *testing.T) {
	state := createApplicationDraft(t, f, map[string]publishing.TranslationPatch{
		"ru": {Title: valueField("Incomplete")}, "pl": completeTranslation("app-draft-pl"),
	})
	_, err := f.editor.MarkTranslationReady(f.ctx, state.ID, "ru", state.Version)
	assertPublishingError(t, err, publishing.Validation)
	state, err = f.editor.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{
		"ru": {Slug: valueField("app-draft-ru"), BodyMarkdown: valueField("Body")},
	})
	requireNoError(t, err)
	state = readyApplicationDraft(t, f, state, "ru")
	before := applicationSnapshot(t, f, state.ID)
	_, err = f.editor.SaveDraft(f.ctx, state.ID, state.Version-1, map[string]publishing.TranslationPatch{"ru": {Title: valueField("Lost")}})
	assertPublishingError(t, err, publishing.Conflict)
	if applicationSnapshot(t, f, state.ID) != before {
		t.Fatal("stale patch changed data")
	}
	unchanged, err := f.editor.MarkTranslationReady(f.ctx, state.ID, "ru", state.Version)
	requireNoError(t, err)
	if unchanged.Version != state.Version {
		t.Fatal("already-ready command changed version")
	}
	state, err = f.editor.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"ru": {Title: publishing.FieldPatch{Set: true}}})
	requireNoError(t, err)
	detail, err := f.editor.GetEditorialDetail(f.ctx, state.ID)
	requireNoError(t, err)
	if len(detail.Drafts) != 2 {
		t.Fatal("partial patch deleted another language")
	}
	for _, d := range detail.Drafts {
		if d.LanguageCode == "ru" && (d.Title != nil || d.Slug == nil || d.BodyMarkdown == nil || d.IsReady) {
			t.Fatal("absent/null or ready reset was lost")
		}
	}
	_, err = f.reader.ResolveArticle(f.ctx, "ru", "app-draft-ru")
	assertPublishingError(t, err, publishing.NotFound)
	_, err = publishing.NewPublicReader(f.owner).ListLanguages(f.ctx)
	assertPublishingError(t, err, publishing.Forbidden)
	_, err = publishing.NewService(f.runtime).CreateDraft(f.ctx, nil)
	assertPublishingError(t, err, publishing.Forbidden)
	languages, err := f.reader.ListLanguages(f.ctx)
	requireNoError(t, err)
	if len(languages) < 3 {
		t.Fatal("public enabled language catalog missing")
	}
	empty, err := f.reader.ListArticles(f.ctx, "de", publishing.Page{Limit: 20})
	requireNoError(t, err)
	if len(empty) != 0 {
		t.Fatal("empty enabled language leaked other language articles")
	}
	_, err = f.reader.ListArticles(f.ctx, "fr", publishing.Page{Limit: 20})
	assertPublishingError(t, err, publishing.NotFound)
}

func (f applicationFixture) testPublication(t *testing.T) {
	state := publishApplicationDraft(t, f, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-first"), "pl": completeTranslation("app-polish")})
	old := *state.PublishedRevisionID
	state, err := f.editor.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{
		"ru": completeTranslation("app-second"), "pl": {BodyMarkdown: valueField("Unfinished Polish")},
	})
	requireNoError(t, err)
	state = readyApplicationDraft(t, f, state, "ru")
	state, err = f.editor.Publish(f.ctx, state.ID, state.Version)
	requireNoError(t, err)
	polish, err := f.reader.ResolveArticle(f.ctx, "pl", "app-polish")
	requireNoError(t, err)
	if polish.Article.BodyMarkdown != "Body app-polish" {
		t.Fatal("Russian update changed public Polish")
	}
	detail, err := f.editor.GetEditorialDetail(f.ctx, state.ID)
	requireNoError(t, err)
	if len(detail.Drafts) != 1 || detail.Drafts[0].LanguageCode != "pl" || *detail.Drafts[0].BodyMarkdown != "Unfinished Polish" {
		t.Fatal("publication lost unfinished draft")
	}
	var oldSlug string
	requireNoError(t, f.owner.QueryRowContext(f.ctx, "SELECT slug FROM public.article_revision_translations WHERE revision_id=$1 AND language_code='ru'", old).Scan(&oldSlug))
	if oldSlug != "app-first" {
		t.Fatal("old immutable snapshot changed")
	}
	assertNoPublicationChange(t, f, state)
	state, err = f.editor.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-second")})
	requireNoError(t, err)
	state = readyApplicationDraft(t, f, state, "ru")
	assertNoPublicationChange(t, f, state)
	first, err := f.reader.ResolveArticle(f.ctx, "ru", "app-first")
	requireNoError(t, err)
	if !first.Redirect || first.Article.Slug != "app-second" {
		t.Fatal("alias did not resolve to current canonical slug")
	}
	state, err = f.editor.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-first")})
	requireNoError(t, err)
	state = readyApplicationDraft(t, f, state, "ru")
	_, err = f.editor.Publish(f.ctx, state.ID, state.Version)
	requireNoError(t, err)
	second, err := f.reader.ResolveArticle(f.ctx, "ru", "app-second")
	requireNoError(t, err)
	if !second.Redirect || second.Article.Slug != "app-first" {
		t.Fatal("return to own old slug failed")
	}
	list, err := f.reader.ListArticles(f.ctx, "ru", publishing.Page{Limit: 100})
	requireNoError(t, err)
	for _, a := range list {
		if a.ArticleID == state.ID && a.Slug != "app-first" {
			t.Fatal("public list exposed historical revision")
		}
	}
	editorial, err := f.editor.GetEditorialList(f.ctx, publishing.Page{Limit: 100})
	requireNoError(t, err)
	if len(editorial) < 2 {
		t.Fatal("editorial list omitted drafts or published articles")
	}
}

// Одинаковый snapshot и отсутствие ready имеют общий запрет фиктивной публикации.
func assertNoPublicationChange(t *testing.T, f applicationFixture, state publishing.ArticleState) {
	t.Helper()
	before := applicationSnapshot(t, f, state.ID)
	_, err := f.editor.Publish(f.ctx, state.ID, state.Version)
	if assertPublishingError(t, err, publishing.Conflict).Code != "no_draft" {
		t.Fatal("publication without a real change returned the wrong conflict")
	}
	if applicationSnapshot(t, f, state.ID) != before {
		t.Fatal("publication without a real change modified data")
	}
}

func (f applicationFixture) testRollback(t *testing.T) {
	_ = publishApplicationDraft(t, f, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-taken")})
	state := createApplicationDraft(t, f, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-taken"), "pl": completeTranslation("app-new-pl")})
	state = readyApplicationDraft(t, f, state, "ru", "pl")
	before := applicationSnapshot(t, f, state.ID)
	_, err := f.editor.Publish(f.ctx, state.ID, state.Version)
	assertPublishingError(t, err, publishing.Conflict)
	if applicationSnapshot(t, f, state.ID) != before {
		t.Fatal("URL collision left partial publication or reserved another language")
	}
	_, err = f.owner.ExecContext(f.ctx, "UPDATE public.languages SET is_enabled=false WHERE code='pl'")
	requireNoError(t, err)
	_, err = f.editor.Publish(f.ctx, state.ID, state.Version)
	assertPublishingError(t, err, publishing.Validation)
	if applicationSnapshot(t, f, state.ID) != before {
		t.Fatal("disabled ready translation caused partial publication")
	}
	_, err = f.owner.ExecContext(f.ctx, "UPDATE public.languages SET is_enabled=true WHERE code='pl'")
	requireNoError(t, err)
}

func (f applicationFixture) testUnpublish(t *testing.T) {
	patch := map[string]publishing.TranslationPatch{"ru": completeTranslation("app-unpub-ru"), "pl": completeTranslation("app-unpub-pl")}
	state := publishApplicationDraft(t, f, patch)
	state, err := f.editor.SaveDraft(f.ctx, state.ID, state.Version, patch)
	requireNoError(t, err)
	state = readyApplicationDraft(t, f, state, "ru", "pl")
	state, err = f.editor.UnpublishArticle(f.ctx, state.ID, state.Version)
	requireNoError(t, err)
	detail, err := f.editor.GetEditorialDetail(f.ctx, state.ID)
	requireNoError(t, err)
	if detail.PublishedRevisionID != nil || len(detail.Drafts) != 2 {
		t.Fatal("unpublish lost draft or retained publication")
	}
	for _, d := range detail.Drafts {
		if d.IsReady {
			t.Fatal("unpublish retained ready flag")
		}
	}
	_, err = f.reader.ResolveArticle(f.ctx, "pl", "app-unpub-pl")
	assertPublishingError(t, err, publishing.NotFound)
	again, err := f.editor.UnpublishArticle(f.ctx, state.ID, state.Version)
	requireNoError(t, err)
	if !reflect.DeepEqual(again, state) {
		t.Fatal("repeated unpublish was not a no-op")
	}
	state = readyApplicationDraft(t, f, state, "ru")
	state, err = f.editor.Publish(f.ctx, state.ID, state.Version)
	requireNoError(t, err)
	if state.PublishedRevisionID == nil {
		t.Fatal("republication missing")
	}
	_, err = f.reader.ResolveArticle(f.ctx, "ru", "app-unpub-ru")
	requireNoError(t, err)
	_, err = f.reader.ResolveArticle(f.ctx, "pl", "app-unpub-pl")
	assertPublishingError(t, err, publishing.NotFound)
}

// Один backend на actor позволяет наблюдать настоящие ожидания без production test hooks.
func startApplicationOperation(t *testing.T, f applicationFixture, name string, call func(*publishing.Service) (publishing.ArticleState, error)) (int, <-chan error, *publishing.ArticleState) {
	t.Helper()
	db := applicationDatabase(t, f.cfg, "sp_publisher", name)
	var pid int
	requireNoError(t, db.QueryRowContext(f.ctx, "SELECT pg_backend_pid()").Scan(&pid))
	state := new(publishing.ArticleState)
	done := make(chan error, 1)
	go func() { var err error; *state, err = call(publishing.NewService(db)); done <- err }()
	return pid, done, state
}

func (f applicationFixture) testVersions(t *testing.T) {
	state := createApplicationDraft(t, f, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-version")})
	barrier := beginRoleTx(t, f.ctx, f.owner, "")
	defer func() { _ = barrier.Rollback() }()
	var locked string
	requireNoError(t, barrier.QueryRowContext(f.ctx, "SELECT id::text FROM public.articles WHERE id=$1 FOR UPDATE", state.ID).Scan(&locked))
	firstPID, first, one := startApplicationOperation(t, f, "publishing-first", func(s *publishing.Service) (publishing.ArticleState, error) {
		return s.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"ru": {Title: valueField("Winner")}})
	})
	waitForLock(t, f.ctx, f.owner, firstPID, first)
	secondPID, second, _ := startApplicationOperation(t, f, "publishing-second", func(s *publishing.Service) (publishing.ArticleState, error) {
		return s.SaveDraft(f.ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"ru": {Title: valueField("Loser")}})
	})
	waitForLock(t, f.ctx, f.owner, secondPID, second)
	requireNoError(t, barrier.Commit())
	requireNoError(t, <-first)
	assertPublishingError(t, <-second, publishing.Conflict)
	detail, err := f.editor.GetEditorialDetail(f.ctx, state.ID)
	requireNoError(t, err)
	if one.Version != state.Version+1 || detail.Version != one.Version || *detail.Drafts[0].Title != "Winner" {
		t.Fatal("concurrent stale editor overwrote committed result")
	}
}

// Незавершённая reservation третьей статьи блокирует обе команды на UNIQUE, не на их article lock.
func reservationBarrier(t *testing.T, f applicationFixture, slug string) *sql.Tx {
	t.Helper()
	other := createApplicationDraft(t, f, nil)
	tx := beginRoleTx(t, f.ctx, f.owner, "")
	execSQL(t, f.ctx, tx, "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('ru',$1,$2)", slug, other.ID)
	return tx
}

func (f applicationFixture) testRoutes(t *testing.T) {
	patch := map[string]publishing.TranslationPatch{"ru": completeTranslation("app-race-url")}
	a := readyApplicationDraft(t, f, createApplicationDraft(t, f, patch), "ru")
	b := readyApplicationDraft(t, f, createApplicationDraft(t, f, patch), "ru")
	beforeA, beforeB := applicationSnapshot(t, f, a.ID), applicationSnapshot(t, f, b.ID)
	barrier := reservationBarrier(t, f, "app-race-url")
	defer func() { _ = barrier.Rollback() }()
	firstPID, first, one := startApplicationOperation(t, f, "publishing-url-first", func(s *publishing.Service) (publishing.ArticleState, error) { return s.Publish(f.ctx, a.ID, a.Version) })
	waitForLock(t, f.ctx, f.owner, firstPID, first)
	secondPID, second, two := startApplicationOperation(t, f, "publishing-url-second", func(s *publishing.Service) (publishing.ArticleState, error) { return s.Publish(f.ctx, b.ID, b.Version) })
	waitForLock(t, f.ctx, f.owner, secondPID, second)
	requireNoError(t, barrier.Rollback())
	errA, errB := <-first, <-second
	loser, before, winner := a, beforeA, two
	if errA == nil {
		loser, before, winner = b, beforeB, one
		assertPublishingError(t, errB, publishing.Conflict)
	} else {
		assertPublishingError(t, errA, publishing.Conflict)
		requireNoError(t, errB)
	}
	if applicationSnapshot(t, f, loser.ID) != before {
		t.Fatal("losing URL publisher retained side effects")
	}
	resolved, err := f.reader.ResolveArticle(f.ctx, "ru", "app-race-url")
	requireNoError(t, err)
	if resolved.Article.ArticleID != winner.ID {
		t.Fatal("unique URL did not belong to the only committed publisher")
	}
}

func (f applicationFixture) testDisable(t *testing.T) {
	t.Run("disable commits before language lock is acquired", f.disableWins)
	t.Run("publication holds language lock until commit", f.publicationWins)
}

func (f applicationFixture) disableWins(t *testing.T) {
	state := readyApplicationDraft(t, f, createApplicationDraft(t, f, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-disabled")}), "ru")
	before := applicationSnapshot(t, f, state.ID)
	barrier := beginRoleTx(t, f.ctx, f.owner, "")
	defer func() { _ = barrier.Rollback() }()
	execSQL(t, f.ctx, barrier, "UPDATE public.languages SET is_enabled=false WHERE code='ru'")
	pid, done, _ := startApplicationOperation(t, f, "publishing-disable-first", func(s *publishing.Service) (publishing.ArticleState, error) {
		return s.Publish(f.ctx, state.ID, state.Version)
	})
	waitForLock(t, f.ctx, f.owner, pid, done)
	requireNoError(t, barrier.Commit())
	assertPublishingError(t, <-done, publishing.Validation)
	if applicationSnapshot(t, f, state.ID) != before {
		t.Fatal("publication used stale enabled state after waiting")
	}
	_, err := f.owner.ExecContext(f.ctx, "UPDATE public.languages SET is_enabled=true WHERE code='ru'")
	requireNoError(t, err)
}

func (f applicationFixture) publicationWins(t *testing.T) {
	state := readyApplicationDraft(t, f, createApplicationDraft(t, f, map[string]publishing.TranslationPatch{"ru": completeTranslation("app-publish-before-disable")}), "ru")
	barrier := reservationBarrier(t, f, "app-publish-before-disable")
	defer func() { _ = barrier.Rollback() }()
	pid, done, published := startApplicationOperation(t, f, "publishing-before-disable", func(s *publishing.Service) (publishing.ArticleState, error) {
		return s.Publish(f.ctx, state.ID, state.Version)
	})
	waitForLock(t, f.ctx, f.owner, pid, done)
	disabler := applicationDatabase(t, f.cfg, "", "publishing-disabler")
	var disablerPID int
	requireNoError(t, disabler.QueryRowContext(f.ctx, "SELECT pg_backend_pid()").Scan(&disablerPID))
	disabled := make(chan error, 1)
	go func() {
		_, err := disabler.ExecContext(f.ctx, "UPDATE public.languages SET is_enabled=false WHERE code='ru'")
		disabled <- err
	}()
	waitForLock(t, f.ctx, f.owner, disablerPID, disabled)
	requireNoError(t, barrier.Rollback())
	requireNoError(t, <-done)
	requireNoError(t, <-disabled)
	if published.PublishedRevisionID == nil {
		t.Fatal("publication did not commit before disable")
	}
	_, err := f.reader.ResolveArticle(f.ctx, "ru", "app-publish-before-disable")
	assertPublishingError(t, err, publishing.NotFound)
	_, err = f.owner.ExecContext(f.ctx, "UPDATE public.languages SET is_enabled=true WHERE code='ru'")
	requireNoError(t, err)
}
