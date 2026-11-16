package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCMSDefaultPrivilegesIntegration(t *testing.T) {
	cfg := cmsDisposableDatabaseConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cluster := Open(cfg)
	t.Cleanup(func() { _ = cluster.Close() })
	name := fmt.Sprintf("stage31_defaults_%d", time.Now().UnixNano())
	databaseName := pgx.Identifier{name}.Sanitize()
	runtimeName, otherRole := name+"_runtime", name+"_other"
	runtimeSQL, otherSQL := pgx.Identifier{runtimeName}.Sanitize(), pgx.Identifier{otherRole}.Sanitize()
	execCMSFixture(t, ctx, cluster, "CREATE ROLE "+runtimeSQL+" LOGIN")
	execCMSFixture(t, ctx, cluster, "CREATE ROLE "+otherSQL+" NOLOGIN")
	execCMSFixture(t, ctx, cluster, "CREATE DATABASE "+databaseName)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		execCMSFixture(t, cleanup, cluster, "DROP DATABASE "+databaseName+" WITH (FORCE)")
		execCMSFixture(t, cleanup, cluster, "DROP ROLE "+runtimeSQL)
		execCMSFixture(t, cleanup, cluster, "DROP ROLE "+otherSQL)
	})
	// Only this uniquely named child database is modified; the supplied fixture survives.
	execCMSFixture(t, ctx, cluster, "REVOKE CREATE,TEMP ON DATABASE "+databaseName+" FROM PUBLIC")
	cfg.Database = name
	db := Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	requireNoError(t, PrepareRoles(ctx, db))
	requireNoError(t, CheckRuntime(ctx, db, runtimeName))
	testCMSDefaultACLBeforeDDL(t, ctx, db, runtimeName, otherSQL)
	requireNoError(t, Up(ctx, db, runtimeName))
	testCMSInstalledDefaultACL(t, ctx, db, otherSQL)
	testCMSUnrelatedDefaultACL(t, ctx, db, otherSQL)
}

func execCMSFixture(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	_, err := db.ExecContext(ctx, query)
	requireNoError(t, err)
}

type cmsDefaultGrant struct{ schema, privilege, objects, recipient string }

const (
	cmsPublicDefaultScope = " IN SCHEMA public"
	cmsDefaultSelect      = "SELECT"
	cmsDefaultTables      = "TABLES"
	cmsDefaultExecute     = "EXECUTE"
	cmsDefaultFunctions   = "FUNCTIONS"
)

func cmsUnsafeDefaultGrants(otherRole string) []cmsDefaultGrant {
	return []cmsDefaultGrant{
		{cmsPublicDefaultScope, cmsDefaultSelect, cmsDefaultTables, publisherTestRole},
		{"", cmsDefaultSelect, cmsDefaultTables, languageImporterTestRole},
		{cmsPublicDefaultScope, "UPDATE", cmsDefaultTables, otherRole},
		{"", cmsDefaultSelect, cmsDefaultTables, "PUBLIC"},
		{cmsPublicDefaultScope, "USAGE", "SEQUENCES", otherRole},
		{"", cmsDefaultSelect, "SEQUENCES", publisherTestRole},
		{cmsPublicDefaultScope, cmsDefaultExecute, cmsDefaultFunctions, otherRole},
		{"", cmsDefaultExecute, cmsDefaultFunctions, languageImporterTestRole},
		{cmsPublicDefaultScope, cmsDefaultExecute, cmsDefaultFunctions, "PUBLIC"},
	}
}

func testCMSDefaultACLBeforeDDL(t *testing.T, ctx context.Context, db *sql.DB, runtime, otherRole string) {
	t.Helper()
	for _, grant := range cmsUnsafeDefaultGrants(otherRole) {
		change := "ALTER DEFAULT PRIVILEGES" + grant.schema
		target := grant.privilege + " ON " + grant.objects
		execCMSFixture(t, ctx, db, change+" GRANT "+target+" TO "+grant.recipient)
		err := Up(ctx, db, runtime)
		execCMSFixture(t, ctx, db, change+" REVOKE "+target+" FROM "+grant.recipient)
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

func testCMSInstalledDefaultACL(t *testing.T, ctx context.Context, db *sql.DB, otherRole string) {
	t.Helper()
	for _, grant := range cmsUnsafeDefaultGrants(otherRole) {
		change := "ALTER DEFAULT PRIVILEGES" + grant.schema
		target := grant.privilege + " ON " + grant.objects
		execCMSFixture(t, ctx, db, change+" GRANT "+target+" TO "+grant.recipient)
		err := CheckSchema(ctx, db)
		execCMSFixture(t, ctx, db, change+" REVOKE "+target+" FROM "+grant.recipient)
		if err == nil {
			t.Fatalf("installed schema ignored unsafe defaults: %+v", grant)
		}
		requireNoError(t, CheckSchema(ctx, db))
	}
}

func testCMSUnrelatedDefaultACL(t *testing.T, ctx context.Context, db *sql.DB, otherRole string) {
	t.Helper()
	execCMSFixture(t, ctx, db, "CREATE SCHEMA unrelated")
	execCMSFixture(t, ctx, db, "ALTER DEFAULT PRIVILEGES IN SCHEMA unrelated GRANT SELECT ON TABLES TO sp_publisher")
	execCMSFixture(t, ctx, db, "ALTER DEFAULT PRIVILEGES FOR ROLE "+otherRole+" IN SCHEMA public GRANT SELECT ON TABLES TO sp_publisher")
	// A global owner ACL row includes PostgreSQL's built-in PUBLIC EXECUTE entry.
	execCMSFixture(t, ctx, db, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO CURRENT_USER WITH GRANT OPTION")
	requireNoError(t, CheckSchema(ctx, db))
}
