package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"stepolonia/apps/api/internal/cms"
)

const cmsLoopbackHostname = "localhost"

func cmsDisposableDatabaseConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	dsn := os.Getenv("STAGE31_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT RUN: STAGE31_TEST_DATABASE_URL is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid disposable database configuration")
	}
	if !strings.HasPrefix(cfg.Database, "stage31_") || (cfg.Host != "127.0.0.1" && cfg.Host != cmsLoopbackHostname) {
		t.Fatal("refusing database outside loopback stage31_ scope")
	}
	return cfg
}

func TestCMSFoundationIntegration(t *testing.T) {
	cfg := cmsDisposableDatabaseConfig(t)
	db := Open(cfg)
	db.SetMaxOpenConns(4)
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	requireNoError(t, PrepareRoles(ctx, db))
	p, err := Provider(db)
	requireNoError(t, err)
	_, err = p.Up(ctx)
	requireNoError(t, err)
	requireNoError(t, CheckSchema(ctx, db))

	t.Run("strict foundation rejects unfinished security paths", func(t *testing.T) { testCMSStrictFoundationRejectsUnfinishedSecurityPaths(t, ctx, db) })
	t.Run("concrete content field boundaries", func(t *testing.T) { testCMSGrantFieldBoundaries(t, ctx, db) })
	t.Run("publisher cannot access dormant CMS", func(t *testing.T) { testCMSPublisherCannotAccessDormantCms(t, ctx, db) })
	t.Run("callback error rolls back and preserves identity", func(t *testing.T) { testCMSCallbackErrorRollsBackAndPreservesIdentity(t, ctx, db) })
	t.Run("statement guard serializes before rows", func(t *testing.T) { testCMSStatementGuardSerializesBeforeRows(t, ctx, db) })
	t.Run("catalog drift fails closed", func(t *testing.T) { testCMSCatalogDriftFailsClosed(t, ctx, db) })
}

func testCMSStrictFoundationRejectsUnfinishedSecurityPaths(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO public.users(id,login_key,state) VALUES ('a0000000-0000-0000-0000-000000000001','invalid-active','active')`,
		`INSERT INTO public.sessions(id,token_digest,csrf_digest,kind,expires_at) VALUES ('a0000000-0000-0000-0000-000000000002',decode(repeat('00',32),'hex'),decode(repeat('01',32),'hex'),'full',CURRENT_TIMESTAMP+interval '1 minute')`,
		`INSERT INTO public.sessions(id,token_digest,csrf_digest,kind,expires_at) VALUES ('a0000000-0000-0000-0000-000000000002',decode(repeat('00',32),'hex'),decode(repeat('01',32),'hex'),'preauth',CURRENT_TIMESTAMP+interval '6 minutes')`,
	} {
		tx, err := db.BeginTx(ctx, nil)
		requireNoError(t, err)
		_, err = tx.ExecContext(ctx, query)
		_ = tx.Rollback()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("expected check violation, got %v", err)
		}
	}
}

func testCMSPublisherCannotAccessDormantCms(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, query := range []string{"SELECT * FROM public.sessions", "SELECT * FROM public.users", "INSERT INTO public.content_types(id,key) VALUES ('a0000000-0000-0000-0000-000000000003','event')"} {
		tx, err := db.BeginTx(ctx, nil)
		requireNoError(t, err)
		_, err = tx.ExecContext(ctx, "SET LOCAL ROLE sp_publisher")
		requireNoError(t, err)
		_, err = tx.ExecContext(ctx, query)
		_ = tx.Rollback()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected insufficient privilege, got %v", err)
		}
	}
}

func testCMSCallbackErrorRollsBackAndPreservesIdentity(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	stop := errors.New("command rejected")
	err := cms.WithWriteMutex(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		var isolation string
		requireNoError(t, tx.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&isolation))
		if isolation != "read committed" {
			t.Fatalf("unsafe isolation: %s", isolation)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO public.users(id,login_key) VALUES ('a0000000-0000-0000-0000-000000000004','rollback-test')`)
		requireNoError(t, err)
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("callback identity lost: %v", err)
	}
	var count int
	requireNoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM public.users WHERE login_key='rollback-test'").Scan(&count))
	if count != 0 {
		t.Fatal("rejected command committed")
	}
}

func testCMSStatementGuardSerializesBeforeRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	first, err := db.BeginTx(ctx, nil)
	requireNoError(t, err)
	defer func() { _ = first.Rollback() }()
	_, err = first.ExecContext(ctx, cms.WriteMutexSQL)
	requireNoError(t, err)
	second, err := db.BeginTx(ctx, nil)
	requireNoError(t, err)
	defer func() { _ = second.Rollback() }()
	_, err = second.ExecContext(ctx, "SET LOCAL lock_timeout='100ms'")
	requireNoError(t, err)
	_, err = second.ExecContext(ctx, `INSERT INTO public.users(id,login_key) VALUES ('a0000000-0000-0000-0000-000000000005','mutex-test')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("statement did not wait on common mutex: %v", err)
	}
}

func testCMSCatalogDriftFailsClosed(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, change := range []struct{ apply, restore string }{
		{"COMMENT ON TABLE public.sessions IS 'drift'", "COMMENT ON TABLE public.sessions IS 'Opaque session digests; never exposed through public views.'"},
		{"GRANT SELECT ON public.sessions TO sp_publisher", "REVOKE ALL ON public.sessions FROM sp_publisher"},
		{"ALTER TABLE public.users DISABLE TRIGGER users_write_mutex", "ALTER TABLE public.users ENABLE TRIGGER users_write_mutex"},
		{"UPDATE public.permission_operations SET scope='installation' WHERE key='content.items.read'", "UPDATE public.permission_operations SET scope='content' WHERE key='content.items.read'"},
	} {
		_, err := db.ExecContext(ctx, change.apply)
		requireNoError(t, err)
		checkErr := CheckSchema(ctx, db)
		_, err = db.ExecContext(ctx, change.restore)
		requireNoError(t, err)
		if checkErr == nil {
			t.Fatalf("drift accepted: %s", change.apply)
		}
		requireNoError(t, CheckSchema(ctx, db))
	}
}

func testCMSGrantFieldBoundaries(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, invalid := range []string{
		`INSERT INTO public.grant_fields(grant_id,type_id,field_id) VALUES ('b0000000-0000-0000-0000-000000000004','b0000000-0000-0000-0000-000000000002','b0000000-0000-0000-0000-000000000003')`,
		`INSERT INTO public.role_grants(id,role_id,operation,scope,type_id) VALUES ('b0000000-0000-0000-0000-000000000005','b0000000-0000-0000-0000-000000000001','content.items.read','content','b0000000-0000-0000-0000-000000000002')`,
	} {
		tx, err := db.BeginTx(ctx, nil)
		requireNoError(t, err)
		_, err = tx.ExecContext(ctx, `
INSERT INTO public.roles(id,name_key) VALUES ('b0000000-0000-0000-0000-000000000001','field-boundary');
INSERT INTO public.content_types(id,key) VALUES ('b0000000-0000-0000-0000-000000000002','field_boundary'),('b0000000-0000-0000-0000-000000000006','other_boundary');
INSERT INTO public.content_fields(type_id,field_id,key) VALUES ('b0000000-0000-0000-0000-000000000006','b0000000-0000-0000-0000-000000000003','foreign_field');
INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) VALUES ('b0000000-0000-0000-0000-000000000004','b0000000-0000-0000-0000-000000000001','content.items.read','content','b0000000-0000-0000-0000-000000000002','any');`)
		requireNoError(t, err)
		_, err = tx.ExecContext(ctx, invalid)
		_ = tx.Rollback()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "23503" && pgErr.Code != "23514") {
			t.Fatalf("expected grant boundary rejection, got %v", err)
		}
	}
}
