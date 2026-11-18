package database

import (
	"context"
	"database/sql"
	"fmt"
	"stepolonia/apps/api/internal/testpostgres"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAdminDefaultPrivilegesIntegration(t *testing.T) {
	cfg := testpostgres.AdminConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cluster := Open(cfg)
	t.Cleanup(func() { _ = cluster.Close() })
	name := fmt.Sprintf("stage31_defaults_%d", time.Now().UnixNano())
	databaseName := pgx.Identifier{name}.Sanitize()
	runtimeName, otherRole := name+"_runtime", name+"_other"
	runtimeSQL, otherSQL := pgx.Identifier{runtimeName}.Sanitize(), pgx.Identifier{otherRole}.Sanitize()
	execAdministrationFixture(t, ctx, cluster, "CREATE ROLE "+runtimeSQL+" LOGIN")
	execAdministrationFixture(t, ctx, cluster, "CREATE ROLE "+otherSQL+" NOLOGIN")
	execAdministrationFixture(t, ctx, cluster, "CREATE DATABASE "+databaseName)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		execAdministrationFixture(t, cleanup, cluster, "DROP DATABASE "+databaseName+" WITH (FORCE)")
		execAdministrationFixture(t, cleanup, cluster, "DROP ROLE "+runtimeSQL)
		execAdministrationFixture(t, cleanup, cluster, "DROP ROLE "+otherSQL)
	})
	// Only this uniquely named child database is modified; the supplied fixture survives.
	execAdministrationFixture(t, ctx, cluster, "REVOKE CREATE,TEMP ON DATABASE "+databaseName+" FROM PUBLIC")
	cfg.Database = name
	db := Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	requireNoError(t, PrepareRoles(ctx, db))
	requireNoError(t, CheckRuntime(ctx, db, runtimeName))
	testAdministrationDefaultACLBeforeDDL(t, ctx, db, runtimeName, otherSQL)
	requireNoError(t, Up(ctx, db, runtimeName))
	testAdministrationInstalledDefaultACL(t, ctx, db, otherSQL)
	testAdministrationUnrelatedDefaultACL(t, ctx, db, otherSQL)
}

func execAdministrationFixture(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	_, err := db.ExecContext(ctx, query)
	requireNoError(t, err)
}

type defaultGrant struct{ schema, privilege, objects, recipient string }

const (
	publicDefaultScope    = " IN SCHEMA public"
	defaultGrantSelect    = "SELECT"
	defaultGrantTables    = "TABLES"
	defaultGrantExecute   = "EXECUTE"
	defaultGrantFunctions = "FUNCTIONS"
)

func unsafeDefaultGrants(otherRole string) []defaultGrant {
	return []defaultGrant{
		{publicDefaultScope, defaultGrantSelect, defaultGrantTables, publisherTestRole},
		{"", defaultGrantSelect, defaultGrantTables, languageImporterTestRole},
		{publicDefaultScope, "UPDATE", defaultGrantTables, otherRole},
		{"", defaultGrantSelect, defaultGrantTables, "PUBLIC"},
		{publicDefaultScope, "USAGE", "SEQUENCES", otherRole},
		{"", defaultGrantSelect, "SEQUENCES", publisherTestRole},
		{publicDefaultScope, defaultGrantExecute, defaultGrantFunctions, otherRole},
		{"", defaultGrantExecute, defaultGrantFunctions, languageImporterTestRole},
		{publicDefaultScope, defaultGrantExecute, defaultGrantFunctions, "PUBLIC"},
	}
}

func testAdministrationDefaultACLBeforeDDL(t *testing.T, ctx context.Context, db *sql.DB, runtime, otherRole string) {
	t.Helper()
	for _, grant := range unsafeDefaultGrants(otherRole) {
		change := "ALTER DEFAULT PRIVILEGES" + grant.schema
		target := grant.privilege + " ON " + grant.objects
		execAdministrationFixture(t, ctx, db, change+" GRANT "+target+" TO "+grant.recipient)
		err := Up(ctx, db, runtime)
		execAdministrationFixture(t, ctx, db, change+" REVOKE "+target+" FROM "+grant.recipient)
		if err == nil {
			t.Fatalf("unsafe defaults accepted before DDL: %+v", grant)
		}
		var created bool
		requireNoError(t, db.QueryRowContext(ctx, `SELECT
 pg_catalog.to_regclass('public.goose_db_version') IS NOT NULL
 OR pg_catalog.to_regclass('public.users') IS NOT NULL
 OR pg_catalog.to_regclass('public.languages') IS NOT NULL`).Scan(&created))
		if created {
			t.Fatalf("preflight failure created schema or history: %+v", grant)
		}
		requireNoError(t, CheckSchema(ctx, db))
	}
}

func testAdministrationInstalledDefaultACL(t *testing.T, ctx context.Context, db *sql.DB, otherRole string) {
	t.Helper()
	for _, grant := range unsafeDefaultGrants(otherRole) {
		change := "ALTER DEFAULT PRIVILEGES" + grant.schema
		target := grant.privilege + " ON " + grant.objects
		execAdministrationFixture(t, ctx, db, change+" GRANT "+target+" TO "+grant.recipient)
		err := CheckSchema(ctx, db)
		execAdministrationFixture(t, ctx, db, change+" REVOKE "+target+" FROM "+grant.recipient)
		if err == nil {
			t.Fatalf("installed schema ignored unsafe defaults: %+v", grant)
		}
		requireNoError(t, CheckSchema(ctx, db))
	}
}

func testAdministrationUnrelatedDefaultACL(t *testing.T, ctx context.Context, db *sql.DB, otherRole string) {
	t.Helper()
	execAdministrationFixture(t, ctx, db, "CREATE SCHEMA unrelated")
	execAdministrationFixture(t, ctx, db, "ALTER DEFAULT PRIVILEGES IN SCHEMA unrelated GRANT SELECT ON TABLES TO sp_publisher")
	execAdministrationFixture(t, ctx, db, "ALTER DEFAULT PRIVILEGES FOR ROLE "+otherRole+" IN SCHEMA public GRANT SELECT ON TABLES TO sp_publisher")
	// A global owner ACL row includes PostgreSQL's built-in PUBLIC EXECUTE entry.
	execAdministrationFixture(t, ctx, db, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO CURRENT_USER WITH GRANT OPTION")
	requireNoError(t, CheckSchema(ctx, db))
}
