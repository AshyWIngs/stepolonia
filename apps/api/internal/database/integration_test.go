package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPublishingSchemaIntegration(t *testing.T) {
	dsn := os.Getenv("STAGE12_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT RUN: STAGE12_TEST_DATABASE_URL is required for disposable PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid disposable database configuration")
	}
	if !strings.HasPrefix(cfg.Database, "stage12_") || (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") {
		t.Fatal("refusing non-disposable database: expected loopback and stage12_ database prefix")
	}
	if path := os.Getenv("STAGE12_TEST_PASSWORD_FILE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("cannot read disposable password")
		}
		cfg.Password = strings.TrimSpace(string(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db := Open(cfg)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("disposable PostgreSQL is unavailable")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(PrepareRoles(ctx, db))
	p, err := Provider(db)
	must(err)
	// The harness may have exercised the CLI first. Return only this disposable
	// schema to version zero, then prove both directions and repeated installation.
	status, err := p.Status(ctx)
	must(err)
	for _, s := range status {
		if s.State == "applied" {
			_, err = p.Down(ctx)
			must(err)
		}
	}
	_, err = p.Up(ctx)
	must(err)
	_, err = p.Down(ctx)
	must(err)
	_, err = p.Up(ctx)
	must(err)
	must(GrantRuntime(ctx, db, "stage12_app"))
	must(Seed(ctx, db))
	must(Seed(ctx, db))
	_, err = db.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code='ru'")
	must(err)
	var serial int
	id := func() string { serial++; return fmt.Sprintf("00000000-0000-0000-0000-%012d", serial) }
	exec := func(tx *sql.Tx, q string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, q, args...)
		must(err)
	}
	begin := func(role string) *sql.Tx {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		t.Cleanup(func() { _ = tx.Rollback() })
		if role != "" {
			exec(tx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize())
		}
		return tx
	}
	rejected := func(role, q, code string, args ...any) {
		t.Helper()
		tx := begin(role)
		_, err := tx.ExecContext(ctx, q, args...)
		assertSQLState(t, err, code)
		_ = tx.Rollback()
	}
	article := id()
	tx := begin("sp_publisher")
	exec(tx, "INSERT INTO public.articles(id)VALUES($1)", article)
	must(tx.Commit())
	t.Run("draft completeness and content edit", func(t *testing.T) {
		rejected("sp_publisher", "INSERT INTO public.article_draft_translations(article_id,language_code,is_ready)VALUES($1,'ru',true)", "23514", article)
		rejected("sp_publisher", "INSERT INTO public.article_draft_translations(article_id,language_code,title,slug,body_markdown,is_ready)VALUES($1,'ru',E'\t\n','ok','body',true)", "23514", article)
		rejected("sp_publisher", "INSERT INTO public.article_draft_translations(article_id,language_code,title,slug,body_markdown,is_ready)VALUES($1,'ru',chr(160),'ok',chr(160),true)", "23514", article)
		tx := begin("sp_publisher")
		exec(tx, "INSERT INTO public.article_draft_translations(article_id,language_code,title,slug,body_markdown,is_ready)VALUES($1,'ru','Title','draft','Body',true)", article)
		exec(tx, "UPDATE public.article_draft_translations SET title='Changed',is_ready=true WHERE article_id=$1", article)
		var ready bool
		must(tx.QueryRowContext(ctx, "SELECT is_ready FROM public.article_draft_translations WHERE article_id=$1", article).Scan(&ready))
		if ready {
			t.Fatal("content edit retained readiness")
		}
		must(tx.Commit())
	})
	t.Run("foreign keys", func(t *testing.T) {
		rejected("sp_publisher", "INSERT INTO public.article_draft_translations(article_id,language_code)VALUES($1,'ru')", "23503", id())
		rejected("sp_publisher", "INSERT INTO public.article_draft_translations(article_id,language_code)VALUES($1,'zzzz')", "23503", article)
		rejected("", "INSERT INTO public.languages(code,name,native_name,direction)VALUES('RU','Russian','Русский','ltr')", "23514")
	})
	t.Run("unsealed commit and empty seal", func(t *testing.T) {
		tx := begin("sp_publisher")
		revision := id()
		exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,1)", revision, article)
		assertSQLState(t, tx.Commit(), "23514")
		tx = begin("sp_publisher")
		exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,1)", id(), article)
		_, err := tx.ExecContext(ctx, "UPDATE public.article_revisions SET sealed=true WHERE article_id=$1", article)
		assertSQLState(t, err, "23514")
		_ = tx.Rollback()
	})
	revision := id()
	tx = begin("sp_publisher")
	exec(tx, "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('ru','original',$1)", article)
	exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,1)", revision, article)
	exec(tx, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'ru','Public','original','Body')", article, revision)
	// A pointer must reject an assembling snapshot even inside the same tx.
	_, err = tx.ExecContext(ctx, "UPDATE public.articles SET published_revision_id=$1 WHERE id=$2", revision, article)
	assertSQLState(t, err, "23514")
	_ = tx.Rollback()
	tx = begin("sp_publisher")
	exec(tx, "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('ru','original',$1)", article)
	exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,1)", revision, article)
	exec(tx, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'ru','Public','original','Body')", article, revision)
	exec(tx, "UPDATE public.article_revisions SET sealed=true WHERE id=$1", revision)
	exec(tx, "UPDATE public.articles SET published_revision_id=$1 WHERE id=$2", revision, article)
	must(tx.Commit())
	t.Run("closed snapshots and permanent reservations", func(t *testing.T) {
		for _, q := range []string{"UPDATE public.article_revision_translations SET title='changed' WHERE revision_id=$1", "DELETE FROM public.article_revision_translations WHERE revision_id=$1", "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown) SELECT article_id,revision_id,'pl','Added','other','Body' FROM public.article_revision_translations WHERE revision_id=$1"} {
			rejected("", q, "23514", revision)
		}
		rejected("sp_publisher", "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown) SELECT article_id,revision_id,'pl','Added','other','Body' FROM public.article_revision_translations WHERE revision_id=$1", "23514", revision)
		rejected("sp_publisher", "UPDATE public.article_revisions SET sealed=false WHERE id=$1", "23514", revision)
		rejected("sp_publisher", "UPDATE public.article_revision_translations SET title='changed' WHERE revision_id=$1", "42501", revision)
		rejected("sp_publisher", "DELETE FROM public.article_revision_translations WHERE revision_id=$1", "42501", revision)
		rejected("", "UPDATE public.article_revisions SET sealed=false WHERE id=$1", "23514", revision)
		rejected("", "DELETE FROM public.article_revisions WHERE id=$1", "23514", revision)
		rejected("", "DELETE FROM public.public_routes WHERE article_id=$1", "23514", article)
		rejected("", "UPDATE public.public_routes SET slug='other' WHERE article_id=$1", "23514", article)
		other := id()
		tx := begin("sp_publisher")
		exec(tx, "INSERT INTO public.articles(id)VALUES($1)", other)
		must(tx.Commit())
		rejected("sp_publisher", "UPDATE public.articles SET published_revision_id=$1 WHERE id=$2", "23514", revision, other)
		rejected("sp_publisher", "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('ru','original',$1)", "23505", other)
		tx = begin("sp_publisher")
		unsealed := id()
		exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,1)", unsealed, other)
		_, err := tx.ExecContext(ctx, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'ru','Title','original','Body')", other, unsealed)
		assertSQLState(t, err, "23503")
		_ = tx.Rollback()
		tx = begin("sp_publisher")
		mismatched := id()
		exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,1)", mismatched, other)
		_, err = tx.ExecContext(ctx, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'ru','Title','original','Body')", article, mismatched)
		assertSQLState(t, err, "23503")
		_ = tx.Rollback()
		tx = begin("sp_publisher")
		exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,2)", id(), article)
		_, err = tx.ExecContext(ctx, "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('ru','original',$1)", other)
		assertSQLState(t, err, "23505")
		_ = tx.Rollback()
		var count int
		must(db.QueryRowContext(ctx, "SELECT count(*) FROM public.article_revisions WHERE article_id=$1", article).Scan(&count))
		if count != 1 {
			t.Fatal("failed publication left revision behind")
		}
	})
	t.Run("uncommitted parent cannot bypass the sealed append guard", func(t *testing.T) {
		routes := begin("sp_publisher")
		exec(routes, "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('pl','concurrent',$1)", article)
		must(routes.Commit())
		assembling := begin("sp_publisher")
		pending := id()
		exec(assembling, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,2)", pending, article)
		exec(assembling, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'ru','Public','original','Body')", article, pending)
		contender := begin("sp_publisher")
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err := contender.ExecContext(bounded, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'pl','Concurrent','concurrent','Body')", article, pending)
		assertSQLState(t, err, "23503")
		_ = contender.Rollback()
		exec(assembling, "UPDATE public.article_revisions SET sealed=true WHERE id=$1", pending)
		must(assembling.Commit())
		var count int
		must(db.QueryRowContext(ctx, "SELECT count(*) FROM public.article_revision_translations WHERE revision_id=$1", pending).Scan(&count))
		if count != 1 {
			t.Fatal("concurrent insert appended to closed snapshot")
		}
	})
	t.Run("catalog import fifth language atomic and enabled preserved", func(t *testing.T) {
		rows, err := ReadLanguages(strings.NewReader("code,name,native_name,direction\nde,German,Deutsch,ltr\nru,Russian updated,Русский,ltr\n"))
		must(err)
		must(ImportLanguages(ctx, db, rows))
		must(ImportLanguages(ctx, db, rows))
		var enabled bool
		must(db.QueryRowContext(ctx, "SELECT is_enabled FROM public.languages WHERE code='ru'").Scan(&enabled))
		if !enabled {
			t.Fatal("import disabled existing language")
		}
		must(db.QueryRowContext(ctx, "SELECT is_enabled FROM public.languages WHERE code='de'").Scan(&enabled))
		if enabled {
			t.Fatal("new language auto-enabled")
		}
		err = ImportLanguages(ctx, db, []Language{{"es", "Spanish", "Español", "ltr"}, {"fr", "French", "Français", "bad"}})
		assertSQLState(t, err, "23514")
		var count int
		must(db.QueryRowContext(ctx, "SELECT count(*) FROM public.languages WHERE code IN ('de','es','fr')").Scan(&count))
		if count != 1 {
			t.Fatal("import left a partial file or duplicate")
		}
		must(db.QueryRowContext(ctx, "SELECT count(*) FROM public.article_revision_translations WHERE revision_id=$1", revision).Scan(&count))
		if count != 1 {
			t.Fatal("import changed existing translations")
		}
	})
	t.Run("runtime SET ROLE membership is rejected even without inheritance", func(t *testing.T) {
		var owner string
		must(db.QueryRowContext(ctx, "SELECT current_user").Scan(&owner))
		grant := "GRANT " + pgx.Identifier{owner}.Sanitize() + " TO stage12_app WITH INHERIT FALSE"
		_, err := db.ExecContext(ctx, grant)
		must(err)
		defer func() {
			_, err := db.ExecContext(ctx, "REVOKE "+pgx.Identifier{owner}.Sanitize()+" FROM stage12_app")
			if err != nil {
				t.Fatal(err)
			}
		}()
		if err := GrantRuntime(ctx, db, "stage12_app"); err == nil {
			t.Fatal("runtime accepted membership that permits SET ROLE")
		}
	})
	t.Run("least privileges", func(t *testing.T) {
		rejected("sp_language_importer", "UPDATE public.languages SET is_enabled=false WHERE code='ru'", "42501")
		rejected("sp_language_importer", "INSERT INTO public.articles(id)VALUES($1)", "42501", id())
		rejected("sp_publisher", "UPDATE public.languages SET is_enabled=false WHERE code='ru'", "42501")
		for _, role := range []string{"stage12_app", "sp_publisher", "sp_language_importer"} {
			rejected(role, "CREATE TABLE public.forbidden(id int)", "42501")
		}
		rejected("stage12_app", "SELECT * FROM public.article_draft_translations", "42501")
		rejected("stage12_app", "INSERT INTO public.articles(id)VALUES($1)", "42501", id())
		rejected("stage12_app", "SELECT * FROM public.lock_languages(ARRAY['ru'])", "42501")
		tx := begin("stage12_app")
		var title string
		must(tx.QueryRowContext(ctx, "SELECT title FROM public.article_revision_translations WHERE revision_id=$1", revision).Scan(&title))
		if title != "Public" {
			t.Fatal("runtime cannot read snapshot")
		}
		must(tx.Commit())
	})
	t.Run("language locks block disable and release on commit", func(t *testing.T) {
		first := begin("sp_publisher")
		var enabled bool
		must(first.QueryRowContext(ctx, "SELECT is_enabled FROM public.lock_languages(ARRAY['ru'])").Scan(&enabled))
		if !enabled {
			t.Fatal("lock did not return enabled row")
		}
		second, err := pgx.ConnectConfig(ctx, cfg)
		must(err)
		defer second.Close(ctx)
		var pid int
		must(second.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid))
		result := make(chan error, 1)
		go func() {
			_, err := second.Exec(ctx, "UPDATE public.languages SET is_enabled=false WHERE code='ru'")
			result <- err
		}()
		waitForLock(t, ctx, db, pid, result)
		must(first.Commit())
		select {
		case err := <-result:
			must(err)
		case <-ctx.Done():
			t.Fatal("disable remained blocked")
		}
		var final bool
		must(db.QueryRowContext(ctx, "SELECT is_enabled FROM public.languages WHERE code='ru'").Scan(&final))
		if final {
			t.Fatal("disable did not commit after release")
		}
	})
}

func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != want {
		t.Fatalf("expected SQLSTATE %s; got %v", want, err)
	}
}

// Observe PostgreSQL's actual lock wait; a timeout only bounds a broken test.
func waitForLock(t *testing.T, ctx context.Context, db *sql.DB, pid int, result <-chan error) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		var blocked bool
		err := db.QueryRowContext(ctx, "SELECT cardinality(pg_catalog.pg_blocking_pids($1)) > 0", pid).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-result:
			t.Fatalf("operation did not wait on language lock: %v", err)
		case <-deadline.C:
			t.Fatal("database lock barrier timed out")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-poll.C:
		}
	}
}
