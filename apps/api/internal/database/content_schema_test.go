package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// Exercise the installation command and preserved site contract on the same
// disposable database used by the end-to-end harness.
func TestContentSchemaIntegration(t *testing.T) {
	cfg := disposableDatabaseConfig(t)
	db := Open(cfg)
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	requireNoError(t, PrepareRoles(ctx, db))
	requireNoError(t, Up(ctx, db, runtimeTestRole))
	var article string
	requireNoError(t, db.QueryRowContext(ctx, "SELECT id::text FROM public.content_types WHERE key='article'").Scan(&article))
	requireNoError(t, Up(ctx, db, runtimeTestRole))
	var same bool
	requireNoError(t, db.QueryRowContext(ctx, "SELECT id=$1::uuid FROM public.content_types WHERE key='article'", article).Scan(&same))
	if !same {
		t.Fatal("repeated Up replaced seeded Collection identity")
	}
	if err := Down(ctx, db, "wrong-marker"); err == nil {
		t.Fatal("unguarded Down accepted")
	}
	requireNoError(t, Down(ctx, db, os.Getenv(disposableTokenEnv)))
	requireNoError(t, CheckSchema(ctx, db))
	requireNoError(t, Up(ctx, db, runtimeTestRole))
	testContentCatalog(t, ctx, db)
	testSiteConfiguration(t, ctx, db, cfg)
}

func testContentCatalog(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"articles", "article_draft_translations", "article_revisions", "article_revision_translations", "public_routes", "public_article_routes", "public_article_translations"} {
		var exists bool
		requireNoError(t, db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists))
		if exists {
			t.Fatal("retired Article-only storage remains")
		}
	}
	var missing int
	requireNoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relkind='r' AND relname<>'goose_db_version' AND COALESCE(pg_catalog.obj_description(oid,'pg_class'),'')=''`).Scan(&missing))
	if missing != 0 {
		t.Fatal("application tables lack meaningful comments")
	}
	for _, query := range []string{"SELECT * FROM public.content_drafts", "SELECT * FROM public.content_field_definitions", "SELECT * FROM public.security_audit"} {
		rejectSQL(t, ctx, db, runtimeTestRole, query, "42501")
	}
	var generation int64
	requireNoError(t, db.QueryRowContext(ctx, "SELECT generation FROM public.languages WHERE code='ru'").Scan(&generation))
	_, err := db.ExecContext(ctx, "UPDATE public.languages SET is_enabled=NOT is_enabled WHERE code='ru'")
	requireNoError(t, err)
	var next int64
	requireNoError(t, db.QueryRowContext(ctx, "SELECT generation FROM public.languages WHERE code='ru'").Scan(&next))
	if next != generation+1 {
		t.Fatal("language transition did not advance generation")
	}
	requireNoError(t, CheckSchema(ctx, db))
}
