package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	siteArticlesKey = "articles"
	siteEmptyKey    = "empty"
)

func siteError(t *testing.T, err error, status int) {
	t.Helper()
	var typed *siteconfig.Error
	if !errors.As(err, &typed) || typed.Status != status {
		t.Fatalf("expected site status %d, got %v", status, err)
	}
}
func registeredFixtureSignatures(t *testing.T, ctx context.Context, owner *sql.DB) map[string][]string {
	t.Helper()
	rows, err := owner.QueryContext(ctx, "SELECT message_key,to_json(placeholders) FROM public.ui_message_keys")
	requireNoError(t, err)
	defer func() { _ = rows.Close() }()
	signatures := make(map[string][]string)
	for rows.Next() {
		var key string
		var encoded []byte
		var placeholders []string
		requireNoError(t, rows.Scan(&key, &encoded))
		requireNoError(t, json.Unmarshal(encoded, &placeholders))
		signatures[key] = placeholders
	}
	requireNoError(t, rows.Err())
	return signatures
}

func siteFixture(signatures map[string][]string) siteconfig.Draft {
	return siteconfig.Draft{
		Settings: siteconfig.Settings{Title: "Fixture title", Tagline: "Fixture tagline", Description: "Fixture description", Notice: "Fixture notice",
			Accent: "#123aBC", DefaultLanguage: "pl", FallbackLanguage: "en"},
		Catalogs: map[string]map[string]string{
			"pl": fixtureMessages("pl", signatures),
			"en": fixtureMessages("en", signatures),
		},
	}
}

func fixtureMessages(code string, signatures map[string][]string) map[string]string {
	messages := make(map[string]string, len(signatures))
	for key, placeholders := range signatures {
		message := "Fixture " + code + " " + key
		for _, placeholder := range placeholders {
			message += " {" + placeholder + "}"
		}
		messages[key] = message
	}
	return messages
}

type siteTestFixture struct {
	signatures  map[string][]string
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
	// Russian is enabled for content, while only English and Polish have complete interface catalogs.
	_, err := owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=code IN ('pl','en','ru') WHERE code IN ('pl','en','ru','kk')")
	requireNoError(t, err)
	f := newApplicationFixture(t, owner, cfg)
	editorDB := applicationDatabase(t, cfg, publisherTestRole, "site-editor")
	editor := siteconfig.NewService(editorDB)
	reader := siteconfig.NewReader(f.runtime)
	server := httptest.NewServer(platform.Handler(httpTestDatabase{f.runtime}, f.reader, slog.New(slog.NewTextHandler(io.Discard, nil)), reader))
	defer server.Close()
	base := server.URL + "/api/v1/site"
	fixture := &siteTestFixture{signatures: registeredFixtureSignatures(t, ctx, owner), owner: owner, cfg: cfg, app: f, editor: editor, reader: reader, server: server, base: base}
	runSiteTests(t, ctx, fixture)
}
func runSiteTests(t *testing.T, ctx context.Context, f *siteTestFixture) {
	t.Run("site missing publication strict HTTP and private access", func(t *testing.T) { f.testAccess(t, ctx) })
	version, err := f.editor.SaveDraft(ctx, 1, siteFixture(f.signatures))
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
	t.Run("site slug rules follow the editable and sealed revisions", func(t *testing.T) { f.testSlugRules(t, ctx) })
	t.Run("site schema drift catches constraints functions views and metadata", func(t *testing.T) { f.testDrift(t, ctx) })
}

func (f *siteTestFixture) testAccess(t *testing.T, ctx context.Context) {
	publicRequest(t, ctx, f.server.Client(), f.base, 503)
	f.testSDK(t, ctx, true)
	for _, q := range []string{"?lang=", "?lang=pl&lang=en", "?x=y", "?version=bad", "?version=00000000x0000x0000x0000x000000000000", "?lang=bad_locale", "?version=", "?lang=%ZZ"} {
		publicRequest(t, ctx, f.server.Client(), f.base+q, 400)
	}
	_, err := siteconfig.NewService(f.app.runtime).SaveDraft(ctx, 1, siteFixture(f.signatures))
	siteError(t, err, 403)
	_, err = siteconfig.NewReader(f.owner).Read(ctx, "", "")
	siteError(t, err, 503)
	for _, table := range []string{"initial_site_defaults", "initial_ui_messages", "initial_slug_rules", "ui_message_keys", "site_state", "site_revisions", "site_revision_keys", "site_revision_languages", "site_revision_messages", "site_revision_slug_rules"} {
		rejectSQL(t, ctx, f.owner, runtimeTestRole, "SELECT * FROM public."+table, "42501")
	}
	rejectSQL(t, ctx, f.owner, publisherTestRole, "SELECT * FROM public.initial_ui_messages", "42501")
	rejectSQL(t, ctx, f.owner, publisherTestRole, "SELECT * FROM public.initial_site_defaults", "42501")
	rejectSQL(t, ctx, f.owner, publisherTestRole, "DELETE FROM public.initial_slug_rules", "42501")
	rejectSQL(t, ctx, f.owner, runtimeTestRole, "SELECT public.ui_placeholders('a')", "42501")
	rejectSQL(t, ctx, f.owner, runtimeTestRole, "SELECT public.valid_slug_rule('ł','l')", "42501")
	rejectSQL(t, ctx, f.owner, publisherTestRole, `INSERT INTO public.site_revisions(id,title,description,tagline,notice,accent,default_language,fallback_language,sealed) VALUES('00000000-0000-0000-0000-000000900001','t','','','','#112233','pl','en',true)`, "23514")
}

func (f *siteTestFixture) testSlugRules(t *testing.T, ctx context.Context) {
	t.Helper()
	initial, err := f.reader.Read(ctx, "", f.publication)
	requireNoError(t, err)
	for _, rule := range []siteconfig.SlugRule{
		{LanguageCode: "pl", Character: "ł", Replacement: "l"},
		{LanguageCode: "kk", Character: "қ", Replacement: "q"},
		{LanguageCode: "ru", Character: "щ", Replacement: "shch"},
	} {
		if !slices.Contains(initial.SlugRules, rule) {
			t.Fatalf("initial settings omit %#v", rule)
		}
	}
	requireNoError(t, ImportLanguages(ctx, f.owner, []Language{{Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr"}}))
	draft := siteFixture(f.signatures)
	draft.SlugRules = []siteconfig.SlugRule{
		{LanguageCode: "RU", Character: "Ё", Replacement: ""},
		{LanguageCode: "pl", Character: "A\u0328", Replacement: "a-1"},
		{LanguageCode: "kk", Character: "Ё", Replacement: "io"},
		{LanguageCode: "fr", Character: "ô", Replacement: "o-2"},
	}
	want := []siteconfig.SlugRule{
		{LanguageCode: "fr", Character: "ô", Replacement: "o-2"},
		{LanguageCode: "kk", Character: "ё", Replacement: "io"},
		{LanguageCode: "pl", Character: "ą", Replacement: "a-1"},
		{LanguageCode: "ru", Character: "ё", Replacement: ""},
	}
	version, err := f.editor.SaveDraft(ctx, f.version, draft)
	requireNoError(t, err)
	visible, err := f.reader.Read(ctx, "", f.publication)
	requireNoError(t, err)
	if !slices.Equal(visible.SlugRules, initial.SlugRules) {
		t.Fatal("published rules followed an unpublished draft")
	}
	assertEditableSlugRules(t, ctx, f.owner, want)
	testInvalidSlugRules(t, ctx, f, version)
	assertEditableSlugRules(t, ctx, f.owner, want)

	version, err = f.editor.SaveDraft(ctx, version, siteFixture(f.signatures))
	requireNoError(t, err)
	publication, version, err := f.editor.Publish(ctx, version)
	requireNoError(t, err)
	visible, err = f.reader.Read(ctx, "", publication)
	requireNoError(t, err)
	if !slices.Equal(visible.SlugRules, want) {
		t.Fatalf("omission dropped normalized rules: %#v", visible.SlugRules)
	}
	rejectSQL(t, ctx, f.owner, publisherTestRole, "UPDATE public.site_revision_slug_rules SET replacement='b' WHERE revision_id=$1", "23514", publication)
	rejectSQL(t, ctx, f.owner, publisherTestRole, "DELETE FROM public.site_revision_slug_rules WHERE revision_id=$1", "23514", publication)
	rejectSQL(t, ctx, f.owner, publisherTestRole, "INSERT INTO public.site_revision_slug_rules VALUES ($1,'pl','ł','l')", "23514", publication)

	draft.SlugRules = []siteconfig.SlugRule{}
	version, err = f.editor.SaveDraft(ctx, version, draft)
	requireNoError(t, err)
	version, err = f.editor.SaveDraft(ctx, version, siteFixture(f.signatures))
	requireNoError(t, err)
	assertEditableSlugRules(t, ctx, f.owner, []siteconfig.SlugRule{})
	f.publication, f.version, err = f.editor.Publish(ctx, version)
	requireNoError(t, err)
	visible, err = f.reader.Read(ctx, "", f.publication)
	requireNoError(t, err)
	if visible.SlugRules == nil || len(visible.SlugRules) != 0 {
		t.Fatalf("empty rules did not publish as an array: %#v", visible.SlugRules)
	}
}

func assertEditableSlugRules(t *testing.T, ctx context.Context, db *sql.DB, want []siteconfig.SlugRule) {
	t.Helper()
	tx := beginRoleTx(t, ctx, db, publisherTestRole)
	editable, err := siteconfig.ReadEditable(ctx, tx)
	requireNoError(t, err)
	requireNoError(t, tx.Commit())
	if !editable.HasDraft || editable.SlugRules == nil || !slices.Equal(editable.SlugRules, want) {
		t.Fatalf("unexpected editable slug rules: draft=%t rules=%#v", editable.HasDraft, editable.SlugRules)
	}
}

func testInvalidSlugRules(t *testing.T, ctx context.Context, f *siteTestFixture, version int64) {
	t.Helper()
	const insert = `INSERT INTO public.site_revision_slug_rules
SELECT draft_id,$1,$2,$3 FROM public.site_state WHERE singleton`
	for _, source := range []string{"\u200d", "\u00a0", "\u0085", "\ue000", "Ą", "a\u0328", "a", "łł"} {
		rejectSQL(t, ctx, f.owner, publisherTestRole, insert, "23514", "pl", source, "a")
	}
	rejectSQL(t, ctx, f.owner, publisherTestRole, insert, "23503", "de", "é", "e")
	rejectSQL(t, ctx, f.owner, publisherTestRole, insert, "23505", "pl", "ą", "a")
	rejectSQL(t, ctx, f.owner, publisherTestRole, insert, "23514", "pl", "ł", "l\n")
	for _, rules := range [][]siteconfig.SlugRule{
		{{LanguageCode: "PL", Character: "Ą", Replacement: "a"}, {LanguageCode: "pl", Character: "A\u0328", Replacement: "b"}},
		{{LanguageCode: "pl", Character: "\u200d", Replacement: "a"}},
		{{LanguageCode: "pl", Character: "ł", Replacement: "L"}},
		{{LanguageCode: "de", Character: "é", Replacement: "e"}},
		make([]siteconfig.SlugRule, 513),
	} {
		draft := siteFixture(f.signatures)
		draft.SlugRules = rules
		_, err := f.editor.SaveDraft(ctx, version, draft)
		siteError(t, err, 400)
	}
}

func (f *siteTestFixture) testPublished(t *testing.T, ctx context.Context) {
	var err error
	body, _ := publicRequest(t, ctx, f.server.Client(), f.base, 200)
	var out siteconfig.Publication
	requireNoError(t, json.Unmarshal(body, &out))
	if out.Version != f.publication || out.Language.Code != "en" || out.Settings.DefaultLanguage != "pl" || !maps.Equal(out.Messages, siteFixture(f.signatures).Catalogs["en"]) || out.Settings.Accent != "#123aBC" {
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
	f.testSDK(t, ctx, false)
}

// Test the exported SDK over HTTP using the restricted PostgreSQL runtime connection.
func (f *siteTestFixture) testSDK(t *testing.T, ctx context.Context, unavailable bool) {
	t.Helper()
	command := exec.CommandContext(ctx, nodeExecutable, "../../../../packages/sdk/tests/site-live.mjs")
	if unavailable {
		command.Args = append(command.Args, "--unavailable")
	}
	signatures, err := json.Marshal(f.signatures)
	requireNoError(t, err)
	command.Env = append(os.Environ(), "STAGE22_API_URL="+f.server.URL, "STAGE22_VERSION="+f.publication,
		"STAGE22_MESSAGE_SIGNATURES="+string(signatures))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("site SDK failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func (f *siteTestFixture) testInvalid(t *testing.T, ctx context.Context) {
	var err error
	for _, mutate := range []func(*siteconfig.Draft){func(d *siteconfig.Draft) { d.Settings.Title = strings.Repeat(" ", 201) + "x" }, func(d *siteconfig.Draft) { d.Settings.Title = "\u00a0\u2003" }, func(d *siteconfig.Draft) { d.Settings.Accent = "red; color: blue" }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = strings.Repeat(" ", 4001) + "x" }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = "\u00a0\u2003" }, func(d *siteconfig.Draft) { d.Settings.DefaultLanguage = "xx-unknown" }, func(d *siteconfig.Draft) { d.Catalogs["en"]["unknown"] = "text" }} {
		d := siteFixture(f.signatures)
		mutate(&d)
		_, err := f.editor.SaveDraft(ctx, f.version, d)
		siteError(t, err, 400)
	}
	for _, mutate := range []func(*siteconfig.Draft){func(d *siteconfig.Draft) { delete(d.Catalogs["en"], siteArticlesKey) }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = "Wrong {name}" }, func(d *siteconfig.Draft) { d.Catalogs["en"][siteArticlesKey] = "Wrong {Name}" }, func(d *siteconfig.Draft) { delete(d.Catalogs, "en") }} {
		d := siteFixture(f.signatures)
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
	d := siteFixture(f.signatures)
	// Quotes and SQL comment markers in editorial text remain bound data.
	sqlLiteral := "x'); DELETE FROM public.site_state; --"
	d.Settings.Notice = sqlLiteral
	for _, m := range d.Catalogs {
		m[siteArticlesKey] = "Welcome {name}"
		m[siteEmptyKey] = sqlLiteral
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
	if out.Settings.Title != "Next title" || out.Messages[siteArticlesKey] != "Welcome {name}" || out.Settings.Notice != sqlLiteral || out.Messages[siteEmptyKey] != sqlLiteral {
		t.Fatal("mixed publication")
	}
	f.publication = next
}

func (f *siteTestFixture) testConcurrent(t *testing.T, ctx context.Context) {
	second := siteconfig.NewService(applicationDatabase(t, f.cfg, publisherTestRole, "site-second-editor"))
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, service := range []*siteconfig.Service{f.editor, second} {
		go func() { <-start; _, e := service.SaveDraft(ctx, f.version, siteFixture(f.signatures)); results <- e }()
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
