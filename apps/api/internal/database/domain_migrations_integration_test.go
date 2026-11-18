package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"stepolonia/apps/api/internal/testpostgres"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
)

const domainQAMarker = "domain-qa-disposable"

func domainQAFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	cfg := testpostgres.AdminConfig(t)
	admin := Open(cfg)
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	requireNoError(t, err)
	id := hex.EncodeToString(suffix)
	name, role := "stage12_domain_"+id, "domain_runtime_"+id
	_, err = admin.Exec("CREATE DATABASE " + pgx.Identifier{name}.Sanitize())
	requireNoError(t, err)
	cfg.Database = name
	db := Open(cfg)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)")
		_, _ = admin.Exec("DROP ROLE " + pgx.Identifier{role}.Sanitize())
		_ = admin.Close()
	})
	for _, query := range []string{
		"COMMENT ON DATABASE " + pgx.Identifier{name}.Sanitize() + " IS 'stepolonia:disposable:" + domainQAMarker + "'",
		"REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{name}.Sanitize() + " FROM PUBLIC",
		"CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " NOLOGIN",
	} {
		_, err = db.Exec(query)
		requireNoError(t, err)
	}
	requireNoError(t, PrepareRoles(context.Background(), db))
	return db, role
}

func domainQASnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var snapshot string
	requireNoError(t, db.QueryRow(`SELECT json_build_array(
 (SELECT string_agg(oid::text||':'||relname,',' ORDER BY oid) FROM pg_class WHERE relnamespace='public'::regnamespace),
 (SELECT string_agg(oid::text||':'||proname,',' ORDER BY oid) FROM pg_proc WHERE pronamespace='public'::regnamespace),
 (SELECT string_agg(row_to_json(h)::text,',' ORDER BY id) FROM public.goose_db_version h))::text`).Scan(&snapshot))
	return snapshot
}

func domainQANamespaceSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var snapshot string
	requireNoError(t, db.QueryRow(`SELECT json_build_array(
 (SELECT string_agg(oid::text||':'||relname,',' ORDER BY oid) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname NOT LIKE 'goose_db_version%'),
 (SELECT string_agg(oid::text||':'||typname,',' ORDER BY oid) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname NOT IN ('goose_db_version','_goose_db_version')),
 (SELECT string_agg(oid::text||':'||proname,',' ORDER BY oid) FROM pg_proc WHERE pronamespace='public'::regnamespace))::text`).Scan(&snapshot))
	return snapshot
}

func TestDomainMigrationsLifecycleIntegration(t *testing.T) {
	db, role := domainQAFixture(t)
	ctx := context.Background()
	requireNoError(t, Up(ctx, db, role))
	before := domainQASnapshot(t, db)
	requireNoError(t, Up(ctx, db, role))
	if domainQASnapshot(t, db) != before {
		t.Fatal("repeated Up changed schema or Goose history")
	}
	if err := Down(ctx, db, "wrong-token"); err == nil {
		t.Fatal("Down accepted wrong disposable token")
	}
	if domainQASnapshot(t, db) != before {
		t.Fatal("rejected Down changed schema or history")
	}
	requireNoError(t, Down(ctx, db, domainQAMarker))
	var objects int
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname NOT LIKE 'goose_db_version%'`).Scan(&objects))
	if objects != 0 {
		t.Fatalf("Down left %d public relations", objects)
	}
	var functions int
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace`).Scan(&functions))
	if functions != 0 {
		t.Fatal("Down left application functions")
	}
	requireNoError(t, CheckSchema(ctx, db))
	requireNoError(t, Down(ctx, db, domainQAMarker))
	requireNoError(t, Up(ctx, db, role))
	requireNoError(t, CheckSchema(ctx, db))
}

func domainQARejectUnchanged(t *testing.T, db *sql.DB, role string) {
	t.Helper()
	before := domainQASnapshot(t, db)
	ctx := context.Background()
	if err := CheckSchema(ctx, db); err == nil {
		t.Fatal("unexpected migration history accepted")
	}
	if err := Up(ctx, db, role); err == nil {
		t.Fatal("Up accepted incompatible history")
	}
	if err := Down(ctx, db, domainQAMarker); err == nil {
		t.Fatal("Down accepted incompatible history")
	}
	if domainQASnapshot(t, db) != before {
		t.Fatal("rejected command changed schema or Goose history")
	}
}

func TestDomainMigrationsPartialPrefixesIntegration(t *testing.T) {
	for version := int64(1); version < finalMigrationVersion; version++ {
		t.Run(fmt.Sprintf("prefix%d", version), func(t *testing.T) {
			db, role := domainQAFixture(t)
			provider, err := Provider(db)
			requireNoError(t, err)
			_, err = provider.UpTo(context.Background(), version)
			requireNoError(t, err)
			domainQARejectUnchanged(t, db, role)
		})
	}
}

func TestDomainMigrationsForeignHistoryIntegration(t *testing.T) {
	for _, versions := range [][]int{{7}, {1, 3}} {
		t.Run(fmt.Sprint(versions), func(t *testing.T) {
			db, role := domainQAFixture(t)
			files := fstest.MapFS{}
			for _, version := range versions {
				files[fmt.Sprintf("%05d_fixture.sql", version)] = &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n")}
			}
			provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
			requireNoError(t, err)
			_, err = provider.Up(context.Background())
			requireNoError(t, err)
			domainQARejectUnchanged(t, db, role)
		})
	}
}

func TestDomainMigrationsLaterConflictPreflightIntegration(t *testing.T) {
	for _, table := range []string{"security_audit", "grant_fields", "content_fields", "site_revisions", "content_item_assignments"} {
		t.Run(table, func(t *testing.T) {
			db, role := domainQAFixture(t)
			_, err := db.Exec("CREATE TABLE public." + pgx.Identifier{table}.Sanitize() + " (foreign_payload text)")
			requireNoError(t, err)
			before := domainQANamespaceSnapshot(t, db)
			if err := Up(context.Background(), db, role); err == nil {
				t.Fatal("Up accepted later-domain foreign object")
			}
			var intact bool
			requireNoError(t, db.QueryRow(`SELECT to_regclass('public.languages') IS NULL AND to_regclass('public.goose_db_version') IS NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='foreign_payload')`, table).Scan(&intact))
			if !intact || domainQANamespaceSnapshot(t, db) != before {
				t.Fatal("preflight changed foreign schema or created earlier domain/history")
			}
		})
	}
}

func TestDomainMigrationsLaterNamespaceConflictIntegration(t *testing.T) {
	cases := map[string]string{
		"index": `CREATE TABLE public.foreign_fixture(id uuid);
CREATE INDEX role_grants_type_idx ON public.foreign_fixture(id)`,
		"composite type name":             `CREATE TYPE public.content_fields AS ENUM ('foreign')`,
		"assignment row type name":        `CREATE TYPE public.content_item_assignments AS (foreign_payload text)`,
		"assignment standalone type name": `CREATE TYPE public.content_item_assignments AS ENUM ('foreign')`,
		"assignment primary index name": `CREATE TABLE public.foreign_fixture(id uuid);
CREATE INDEX content_item_assignments_pkey ON public.foreign_fixture(id)`,
		"assignment user index name": `CREATE TABLE public.foreign_fixture(id uuid);
CREATE INDEX content_item_assignments_user_idx ON public.foreign_fixture(id)`,
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			db, role := domainQAFixture(t)
			_, err := db.Exec(setup)
			requireNoError(t, err)
			before := domainQANamespaceSnapshot(t, db)
			if err := Up(context.Background(), db, role); err == nil {
				t.Fatal("Up accepted later-domain namespace collision")
			}
			var untouched bool
			requireNoError(t, db.QueryRow(`SELECT to_regclass('public.languages') IS NULL
AND to_regclass('public.goose_db_version') IS NULL`).Scan(&untouched))
			if !untouched {
				t.Fatal("namespace collision allowed earlier domain or history DDL")
			}
			after := domainQANamespaceSnapshot(t, db)
			if before != after {
				t.Fatal("preflight changed foreign object identities")
			}
		})
	}
}
