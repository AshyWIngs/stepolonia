package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
)

const siteArticlesKey = "articles"

func siteError(t *testing.T, err error, status int) {
	t.Helper()
	var typed *siteconfig.Error
	if !errors.As(err, &typed) || typed.Status != status {
		t.Fatalf("expected site status %d, got %v", status, err)
	}
}
func siteFixture() siteconfig.Draft {
	keys := []string{siteArticlesKey, "empty", "pagination", "previous", "next", "navigation", "all", "languages", "errors.400", "errors.404", "errors.503", "errors.other", "errors.retry", "errors.home"}
	catalogs := map[string]map[string]string{}
	for _, code := range []string{"pl", "en"} {
		catalogs[code] = map[string]string{}
		for _, key := range keys {
			catalogs[code][key] = "Fixture " + code + " " + key
		}
	}
	return siteconfig.Draft{Settings: siteconfig.Settings{Title: "Fixture title", Tagline: "Fixture tagline", Description: "Fixture description", Notice: "Fixture notice", Accent: "#123aBC", DefaultLanguage: "pl", FallbackLanguage: "en"}, Catalogs: catalogs}
}

type siteTestFixture struct {
	owner       *sql.DB
	cfg         *pgx.ConnConfig
	app         applicationFixture
	editor      *siteconfig.Service
	reader      *siteconfig.Reader
	server      *httptest.Server
	base        string
	version     int64
	publication string
}

func testSiteConfiguration(t *testing.T, ctx context.Context, owner *sql.DB, cfg *pgx.ConnConfig) {
	_, err := owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code IN ('pl','en')")
	requireNoError(t, err)
	f := newApplicationFixture(t, owner, cfg)
	editorDB := applicationDatabase(t, cfg, publisherTestRole, "site-editor")
	editor := siteconfig.NewService(editorDB)
	reader := siteconfig.NewReader(f.runtime)
	server := httptest.NewServer(platform.Handler(httpTestDatabase{f.runtime}, f.reader, slog.New(slog.NewTextHandler(io.Discard, nil)), reader))
	defer server.Close()
	base := server.URL + "/api/v1/site"
	fixture := &siteTestFixture{owner: owner, cfg: cfg, app: f, editor: editor, reader: reader, server: server, base: base}
	runSiteTests(t, ctx, fixture)
}
func runSiteTests(t *testing.T, ctx context.Context, f *siteTestFixture) {
	t.Run("site missing publication strict HTTP and private access", func(t *testing.T) { f.testAccess(t, ctx) })
	version, err := f.editor.SaveDraft(ctx, 1, siteFixture())
	requireNoError(t, err)
	publicRequest(t, ctx, f.server.Client(), f.base, 503)
	publication, version, err := f.editor.Publish(ctx, version)
	f.publication = publication
	f.version = version
	requireNoError(t, err)
	t.Run("site atomic published contract fallback and immutable snapshots", func(t *testing.T) { f.testPublished(t, ctx) })
	t.Run("site invalid drafts and failed publication preserve visible revision", func(t *testing.T) { f.testInvalid(t, ctx) })
	t.Run("site required placeholder signatures and stale versions", func(t *testing.T) { f.testPlaceholders(t, ctx) })
	t.Run("site concurrent optimistic writes", func(t *testing.T) { f.testConcurrent(t, ctx) })
	t.Run("site disabled fallback is diagnostic and failed publish is atomic", func(t *testing.T) { f.testDisabled(t, ctx) })
	t.Run("site schema drift catches constraints functions views and metadata", func(t *testing.T) { f.testDrift(t, ctx) })
}

func (f *siteTestFixture) testAccess(t *testing.T, ctx context.Context) {
	publicRequest(t, ctx, f.server.Client(), f.base, 503)
	for _, q := range []string{"?lang=", "?lang=pl&lang=en", "?x=y", "?version=bad", "?version=00000000x0000x0000x0000x000000000000", "?lang=bad_locale", "?version=", "?lang=%ZZ"} {
		publicRequest(t, ctx, f.server.Client(), f.base+q, 400)
	}
	_, err := siteconfig.NewService(f.app.runtime).SaveDraft(ctx, 1, siteFixture())
	siteError(t, err, 403)
	_, err = siteconfig.NewReader(f.owner).Read(ctx, "", "")
	siteError(t, err, 503)
	for _, table := range []string{"ui_message_keys", "site_state", "site_revisions", "site_revision_keys", "site_revision_languages", "site_revision_messages"} {
		rejectSQL(t, ctx, f.owner, runtimeTestRole, "SELECT * FROM public."+table, "42501")
	}
	rejectSQL(t, ctx, f.owner, runtimeTestRole, "SELECT public.ui_placeholders('a')", "42501")
	rejectSQL(t, ctx, f.owner, publisherTestRole, `INSERT INTO public.site_revisions(id,title,description,tagline,notice,accent,default_language,fallback_language,sealed) VALUES('00000000-0000-0000-0000-000000900001','t','','','','#112233','pl','en',true)`, "23514")
}

func (f *siteTestFixture) testPublished(t *testing.T, ctx context.Context) {
	var err error
	body, _ := publicRequest(t, ctx, f.server.Client(), f.base, 200)
	var out siteconfig.Publication
	requireNoError(t, json.Unmarshal(body, &out))
	if out.Version != f.publication || out.Language.Code != "en" || out.Settings.DefaultLanguage != "pl" || len(out.Messages) != 14 || out.Settings.Accent != "#123aBC" {
		t.Fatalf("unexpected site contract: %s", body)
	}
	out, err = f.reader.Read(ctx, "PL", f.publication)
	requireNoError(t, err)
	if out.Language.Code != "pl" || out.RequestedLanguage != "pl" {
		t.Fatal("selected language missing")
	}
	out, err = f.reader.Read(ctx, "fr", "")
	requireNoError(t, err)
	if out.Language.Code != "en" || out.RequestedLanguage != "fr" {
		t.Fatal("fallback missing")
	}
	rejectSQL(t, ctx, f.owner, publisherTestRole, "UPDATE public.site_revisions SET title='changed' WHERE id=$1", "23514", f.publication)
	rejectSQL(t, ctx, f.owner, publisherTestRole, "DELETE FROM public.site_revision_messages WHERE revision_id=$1", "23514", f.publication)
	rejectSQL(t, ctx, f.owner, publisherTestRole, "UPDATE public.site_revision_keys SET placeholders=ARRAY['x'] WHERE revision_id=$1", "23514", f.publication)
	rejectSQL(t, ctx, f.owner, publisherTestRole, "UPDATE public.site_revision_languages SET direction='rtl' WHERE revision_id=$1", "23514", f.publication)
	_, _, err = f.editor.Publish(ctx, f.version)
	siteError(t, err, 409)
}

func (f *siteTestFixture) testInvalid(t *testing.T, ctx context.Context) {
	var err error
	for _, mutate := range []func(*siteconfig.Draft){func(d *siteconfig.Draft) { d.Settings.Title = strings.Repeat(" ", 201) + "x" }, func(d *siteconfig.Draft) { d.Settings.Title = "\u00a0\u2003" }, func(d *siteconfig.Draft) { d.Settings.Accent = "red; color: blue" }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = strings.Repeat(" ", 4001) + "x" }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = "\u00a0\u2003" }, func(d *siteconfig.Draft) { d.Settings.DefaultLanguage = "xx-unknown" }, func(d *siteconfig.Draft) { d.Catalogs["en"]["unknown"] = "text" }} {
		d := siteFixture()
		mutate(&d)
		_, err := f.editor.SaveDraft(ctx, f.version, d)
		siteError(t, err, 400)
	}
	for _, mutate := range []func(*siteconfig.Draft){func(d *siteconfig.Draft) { delete(d.Catalogs["en"], siteArticlesKey) }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = "Wrong {name}" }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = "Wrong {Name}" }, func(d *siteconfig.Draft) { delete(d.Catalogs, "en") }} {
		d := siteFixture()
		mutate(&d)
		f.version, err = f.editor.SaveDraft(ctx, f.version, d)
		requireNoError(t, err)
		_, _, err = f.editor.Publish(ctx, f.version)
		siteError(t, err, 400)
		out, err := f.reader.Read(ctx, "", f.publication)
		requireNoError(t, err)
		if out.Version != f.publication || out.Settings.Title != "Fixture title" {
			t.Fatal("failed publication changed public state")
		}
	}
}

func (f *siteTestFixture) testPlaceholders(t *testing.T, ctx context.Context) {
	_, err := f.owner.ExecContext(ctx, "UPDATE public.ui_message_keys SET placeholders=ARRAY['name'] WHERE message_key='articles'")
	requireNoError(t, err)
	defer func() {
		_, e := f.owner.ExecContext(ctx, "UPDATE public.ui_message_keys SET placeholders='{}' WHERE message_key='articles'")
		requireNoError(t, e)
	}()
	d := siteFixture()
	for _, m := range d.Catalogs {
		m[siteArticlesKey] = "Welcome {name}"
	}
	d.Settings.Title = "Next title"
	f.version, err = f.editor.SaveDraft(ctx, f.version, d)
	requireNoError(t, err)
	next, newVersion, err := f.editor.Publish(ctx, f.version)
	requireNoError(t, err)
	f.version = newVersion
	if next == f.publication {
		t.Fatal("publication token reused")
	}
	publicRequest(t, ctx, f.server.Client(), f.base+"?version="+f.publication, 409)
	out, err := f.reader.Read(ctx, "en", next)
	requireNoError(t, err)
	if out.Settings.Title != "Next title" || out.Messages[siteArticlesKey] != "Welcome {name}" {
		t.Fatal("mixed publication")
	}
	f.publication = next
}

func (f *siteTestFixture) testConcurrent(t *testing.T, ctx context.Context) {
	second := siteconfig.NewService(applicationDatabase(t, f.cfg, publisherTestRole, "site-second-editor"))
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, service := range []*siteconfig.Service{f.editor, second} {
		go func() { <-start; _, e := service.SaveDraft(ctx, f.version, siteFixture()); results <- e }()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			successes++
		} else {
			siteError(t, e, 409)
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("optimistic write did not select one winner")
	}
	f.version++
}

func (f *siteTestFixture) testDisabled(t *testing.T, ctx context.Context) {
	_, err := f.owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=false WHERE code='en'")
	requireNoError(t, err)
	publicRequest(t, ctx, f.server.Client(), f.base, 503)
	_, _, err = f.editor.Publish(ctx, f.version)
	siteError(t, err, 400)
	_, err = f.owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code='en'")
	requireNoError(t, err)
	out, err := f.reader.Read(ctx, "", f.publication)
	requireNoError(t, err)
	if out.Version != f.publication {
		t.Fatal("previous publication lost")
	}
}

func (f *siteTestFixture) testDrift(t *testing.T, ctx context.Context) {
	for _, pair := range [][2]string{{"ALTER TABLE public.site_revisions DISABLE TRIGGER site_revisions_guard", "ALTER TABLE public.site_revisions ENABLE TRIGGER site_revisions_guard"}, {"ALTER VIEW public.public_site SET (security_barrier=false)", "ALTER VIEW public.public_site SET (security_barrier=true)"}, {"ALTER TABLE public.site_revision_messages ADD COLUMN unexpected text", "ALTER TABLE public.site_revision_messages DROP COLUMN unexpected"}} {
		_, err := f.owner.ExecContext(ctx, pair[0])
		requireNoError(t, err)
		if err = CheckSchema(ctx, f.owner); err == nil {
			t.Fatal("site schema drift accepted")
		}
		_, err = f.owner.ExecContext(ctx, pair[1])
		requireNoError(t, err)
	}
	requireNoError(t, CheckSchema(ctx, f.owner))
}
