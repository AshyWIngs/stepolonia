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
	// Сбрасываем только помеченную одноразовую БД, созданную нашим harness.
	status, err := p.Status(ctx)
	must(err)
	for _, state := range status {
		if state.State == "applied" {
			must(Down(ctx, db, os.Getenv("DB_DISPOSABLE_TOKEN")))
		}
	}
	t.Run("fresh conflict metadata repeat up and guarded down", func(t *testing.T) {
		// Default ACL не должны открывать runtime доступ к приватным объектам установки.
		_, err := db.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO stage12_app")
		must(err)
		const defaultACL = `SELECT COALESCE(json_agg(row_to_json(d) ORDER BY oid)::text,'[]') FROM pg_default_acl d WHERE defaclrole=current_user::regrole`
		var beforeDefaults, afterDefaults string
		must(db.QueryRowContext(ctx, defaultACL).Scan(&beforeDefaults))
		if err := Up(ctx, db, "stage12_app"); err == nil {
			t.Fatal("unsafe default ACL accepted before initial DDL")
		}
		must(db.QueryRowContext(ctx, defaultACL).Scan(&afterDefaults))
		var noDDL bool
		must(db.QueryRowContext(ctx, "SELECT to_regclass('public.languages') IS NULL AND to_regclass('public.articles') IS NULL AND (SELECT max(version_id)=0 FROM public.goose_db_version WHERE is_applied)").Scan(&noDDL))
		if !noDDL || beforeDefaults != afterDefaults {
			t.Fatal("default ACL preflight modified schema or defaults")
		}
		_, err = db.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM stage12_app")
		must(err)
		_, err = db.ExecContext(ctx, "CREATE TABLE public.languages (unexpected integer)")
		must(err)
		if err := Up(ctx, db, "stage12_app"); err == nil {
			t.Fatal("unexpected object accepted without migration history")
		}
		var untouched bool
		must(db.QueryRowContext(ctx, "SELECT to_regclass('public.articles') IS NULL AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='public.languages'::regclass AND attname='unexpected')").Scan(&untouched))
		if !untouched {
			t.Fatal("failed preflight changed schema")
		}
		_, err = db.ExecContext(ctx, "DROP TABLE public.languages")
		must(err)
		must(Up(ctx, db, "stage12_app"))
		must(Seed(ctx, db))
		_, err = db.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code='ru'")
		must(err)
		must(Up(ctx, db, "stage12_app"))
		var enabled bool
		must(db.QueryRowContext(ctx, "SELECT is_enabled FROM public.languages WHERE code='ru'").Scan(&enabled))
		if !enabled {
			t.Fatal("repeated up changed enabled-language data")
		}
		var missing int
		must(db.QueryRowContext(ctx, `SELECT count(*) FROM (
		 SELECT pg_catalog.obj_description(c.oid,'pg_class') AS description
		 FROM pg_catalog.pg_class c WHERE c.relnamespace='public'::regnamespace AND c.relname IN
		 ('languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes','public_languages','public_article_translations','public_article_routes')
		 UNION ALL SELECT pg_catalog.col_description(c.oid,a.attnum)
		 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
		 WHERE c.relnamespace='public'::regnamespace AND c.relname IN
		 ('languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes','public_languages','public_article_translations','public_article_routes')
		 UNION ALL SELECT pg_catalog.obj_description(p.oid,'pg_proc') FROM pg_catalog.pg_proc p
		 WHERE p.pronamespace='public'::regnamespace AND p.proname IN
		 ('reset_draft_ready','guard_revision_translation','guard_revision','require_closed_revision','guard_published_revision','keep_route_reservation','lock_languages')
		 UNION ALL SELECT pg_catalog.obj_description(t.oid,'pg_trigger') FROM pg_catalog.pg_trigger t
		 JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace='public'::regnamespace AND NOT t.tgisinternal
		 ) descriptions WHERE description IS NULL OR description !~ '[А-Яа-я]'`).Scan(&missing))
		if missing != 0 {
			t.Fatalf("missing meaningful Russian catalog descriptions: %d", missing)
		}
		for _, token := range []string{"", "incorrect-token"} {
			if err := Down(ctx, db, token); err == nil {
				t.Fatal("unguarded down succeeded")
			}
		}
		var version int
		must(db.QueryRowContext(ctx, "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied").Scan(&version))
		if version != 1 {
			t.Fatal("rejected down changed migration history")
		}
		must(Down(ctx, db, os.Getenv("DB_DISPOSABLE_TOKEN")))
		must(CheckSchema(ctx, db))
		must(db.QueryRowContext(ctx, "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied").Scan(&version))
		if version != 0 {
			t.Fatal("down did not return goose history to version zero")
		}
		must(Up(ctx, db, "stage12_app"))
	})
	t.Run("repeated up rejects changed column and removed guard before DDL", func(t *testing.T) {
		_, err := db.ExecContext(ctx, "ALTER TABLE public.articles ALTER COLUMN version TYPE integer")
		must(err)
		if err := Up(ctx, db, "stage12_app"); err == nil {
			t.Fatal("changed column accepted")
		}
		var typ string
		must(db.QueryRowContext(ctx, "SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='public.articles'::regclass AND attname='version'").Scan(&typ))
		if typ != "integer" {
			t.Fatal("preflight attempted to repair column drift")
		}
		_, err = db.ExecContext(ctx, "ALTER TABLE public.articles ALTER COLUMN version TYPE bigint")
		must(err)
		_, err = db.ExecContext(ctx, "DROP TRIGGER articles_published_guard ON public.articles")
		must(err)
		if err := Up(ctx, db, "stage12_app"); err == nil {
			t.Fatal("removed publication guard accepted")
		}
		var count int
		must(db.QueryRowContext(ctx, "SELECT count(*) FROM pg_trigger WHERE tgrelid='public.articles'::regclass AND tgname='articles_published_guard'").Scan(&count))
		if count != 0 {
			t.Fatal("preflight attempted to repair missing trigger")
		}
		_, err = db.ExecContext(ctx, "CREATE TRIGGER articles_published_guard BEFORE INSERT OR UPDATE OF published_revision_id,id ON public.articles FOR EACH ROW EXECUTE FUNCTION public.guard_published_revision()")
		must(err)
		_, err = db.ExecContext(ctx, "COMMENT ON TRIGGER articles_published_guard ON public.articles IS 'Разрешает публикацию только собственной закрытой ревизии статьи.'")
		must(err)
		must(Up(ctx, db, "stage12_app"))
	})
	t.Run("semantic drift fails before catalog history or grants change", func(t *testing.T) {
		var view, guard, check, fk string
		must(db.QueryRowContext(ctx, "SELECT pg_get_viewdef('public.public_article_translations'::regclass,false)").Scan(&view))
		must(db.QueryRowContext(ctx, "SELECT pg_get_functiondef('public.guard_published_revision()'::regprocedure)").Scan(&guard))
		must(db.QueryRowContext(ctx, "SELECT pg_get_constraintdef(oid,false) FROM pg_constraint WHERE conrelid='public.articles'::regclass AND conname='articles_version_check'").Scan(&check))
		must(db.QueryRowContext(ctx, "SELECT pg_get_constraintdef(oid,false) FROM pg_constraint WHERE conrelid='public.articles'::regclass AND conname='articles_published_revision_fkey'").Scan(&fk))
		// Снимок содержит только структуру и права одноразовой БД, без прикладных данных.
		const snapshotSQL = `SELECT json_build_object(
		 'history',(SELECT json_agg(row_to_json(v) ORDER BY id) FROM public.goose_db_version v),
		 'relations',(SELECT json_agg(json_build_array(relname,oid,relowner,relacl,reloptions) ORDER BY relname) FROM pg_class WHERE relnamespace='public'::regnamespace),
		 'constraints',(SELECT json_agg(json_build_array(c.conname,pg_get_constraintdef(c.oid,false)) ORDER BY c.oid) FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE t.relnamespace='public'::regnamespace),
		 'columns',(SELECT json_agg(json_build_array(c.relname,a.attname,a.atttypid,a.attnotnull,a.attcollation,pg_get_expr(d.adbin,d.adrelid,false)) ORDER BY c.oid,a.attnum) FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE c.relnamespace='public'::regnamespace),
		 'views',(SELECT json_agg(json_build_array(relname,pg_get_viewdef(oid,false)) ORDER BY oid) FROM pg_class WHERE relnamespace='public'::regnamespace AND relkind='v'),
		 'functions',(SELECT json_agg(json_build_array(oid,proacl,pg_get_functiondef(oid)) ORDER BY oid) FROM pg_proc WHERE pronamespace='public'::regnamespace)
		 )::text`
		for _, change := range []struct{ name, mutation, restore string }{
			{"all history view", `CREATE OR REPLACE VIEW public.public_article_translations WITH (security_barrier=true) AS SELECT article_id,revision_id,language_code,title,slug,body_markdown FROM public.article_revision_translations`, "CREATE OR REPLACE VIEW public.public_article_translations WITH (security_barrier=true) AS " + view},
			{"same name weakened check", "ALTER TABLE public.articles DROP CONSTRAINT articles_version_check; ALTER TABLE public.articles ADD CONSTRAINT articles_version_check CHECK(version>=0)", "ALTER TABLE public.articles DROP CONSTRAINT articles_version_check; ALTER TABLE public.articles ADD CONSTRAINT articles_version_check " + check},
			{"same name weakened ownership FK", "ALTER TABLE public.articles DROP CONSTRAINT articles_published_revision_fkey; ALTER TABLE public.articles ADD CONSTRAINT articles_published_revision_fkey FOREIGN KEY(published_revision_id) REFERENCES public.article_revisions(id)", "ALTER TABLE public.articles DROP CONSTRAINT articles_published_revision_fkey; ALTER TABLE public.articles ADD CONSTRAINT articles_published_revision_fkey " + fk},
			{"publication guard body", `CREATE OR REPLACE FUNCTION public.guard_published_revision() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$ BEGIN RETURN NEW; END; $$`, guard},
			{"new languages enabled by default", "ALTER TABLE public.languages ALTER COLUMN is_enabled SET DEFAULT true", "ALTER TABLE public.languages ALTER COLUMN is_enabled SET DEFAULT false"},
			{"extra required column", "ALTER TABLE public.articles ADD COLUMN unexpected_required integer NOT NULL", "ALTER TABLE public.articles DROP COLUMN unexpected_required"},
			{"view security invoker", "ALTER VIEW public.public_article_translations SET(security_invoker=true)", "ALTER VIEW public.public_article_translations RESET(security_invoker)"},
			{"runtime definer function privilege", "GRANT EXECUTE ON FUNCTION public.lock_languages(text[]) TO stage12_app", "REVOKE EXECUTE ON FUNCTION public.lock_languages(text[]) FROM stage12_app"},
		} {
			t.Run(change.name, func(t *testing.T) {
				_, err := db.ExecContext(ctx, change.mutation)
				must(err)
				var before, after string
				must(db.QueryRowContext(ctx, snapshotSQL).Scan(&before))
				if err := Up(ctx, db, "stage12_app"); err == nil {
					t.Fatal("semantic drift accepted")
				}
				must(db.QueryRowContext(ctx, snapshotSQL).Scan(&after))
				if before != after {
					t.Fatal("failed preflight changed catalog history or grants")
				}
				_, err = db.ExecContext(ctx, change.restore)
				must(err)
				must(CheckSchema(ctx, db))
			})
		}
	})
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
		for _, table := range []string{"languages", "articles", "article_draft_translations", "article_revisions", "article_revision_translations", "public_routes"} {
			rejected("stage12_app", "SELECT * FROM public."+table, "42501")
		}
		rejected("stage12_app", "INSERT INTO public.articles(id)VALUES($1)", "42501", id())
		rejected("stage12_app", "SELECT * FROM public.lock_languages(ARRAY['ru'])", "42501")
		tx := begin("stage12_app")
		var title string
		must(tx.QueryRowContext(ctx, "SELECT title FROM public.public_article_translations WHERE revision_id=$1", revision).Scan(&title))
		if title != "Public" {
			t.Fatal("runtime cannot read snapshot")
		}
		must(tx.Commit())
	})
	t.Run("runtime sees current publication aliases and enabled languages only", func(t *testing.T) {
		current := id()
		tx := begin("sp_publisher")
		exec(tx, "INSERT INTO public.public_routes(language_code,slug,article_id)VALUES('ru','current',$1),('de','disabled',$1)", article)
		exec(tx, "INSERT INTO public.article_revisions(id,article_id,revision_no)VALUES($1,$2,3)", current, article)
		exec(tx, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown)VALUES($1,$2,'ru','Current','current','Current body'),($1,$2,'de','Disabled','disabled','Hidden body')", article, current)
		exec(tx, "UPDATE public.article_revisions SET sealed=true WHERE id=$1", current)
		exec(tx, "UPDATE public.articles SET published_revision_id=$1 WHERE id=$2", current, article)
		must(tx.Commit())
		// Язык старого alias включён, но в текущей ревизии перевода на нём нет.
		_, err := db.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code='pl'")
		must(err)
		tx = begin("stage12_app")
		var title, slug string
		must(tx.QueryRowContext(ctx, "SELECT title,slug FROM public.public_article_translations WHERE article_id=$1", article).Scan(&title, &slug))
		if title != "Current" || slug != "current" {
			t.Fatal("runtime exposed historical or disabled translation")
		}
		var translations, routes, languages int
		must(tx.QueryRowContext(ctx, "SELECT count(*) FROM public.public_article_translations WHERE article_id=$1", article).Scan(&translations))
		must(tx.QueryRowContext(ctx, "SELECT count(*) FROM public.public_article_routes WHERE article_id=$1 AND current_slug='current' AND revision_id=$2", article, current).Scan(&routes))
		must(tx.QueryRowContext(ctx, "SELECT count(*) FROM public.public_languages WHERE code='de'").Scan(&languages))
		if translations != 1 || routes != 2 || languages != 0 {
			t.Fatal("read model exposed disabled language or unresolved historical route")
		}
		must(tx.Commit())
		for _, state := range []struct {
			revision any
			enabled  bool
		}{{nil, true}, {current, false}} {
			fixture := begin("")
			exec(fixture, "UPDATE public.articles SET published_revision_id=$1 WHERE id=$2", state.revision, article)
			exec(fixture, "UPDATE public.languages SET is_enabled=$1 WHERE code='ru'", state.enabled)
			must(fixture.Commit())
			tx = begin("stage12_app")
			must(tx.QueryRowContext(ctx, "SELECT count(*) FROM public.public_article_translations").Scan(&translations))
			must(tx.QueryRowContext(ctx, "SELECT count(*) FROM public.public_article_routes").Scan(&routes))
			if translations != 0 || routes != 0 {
				t.Fatal("runtime exposed unpublished or disabled content")
			}
			must(tx.Commit())
		}
		_, err = db.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code='ru'")
		must(err)
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
