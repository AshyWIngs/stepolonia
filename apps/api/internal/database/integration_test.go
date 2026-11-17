package database

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"strings"
	"testing"
)

const (
	disposableTokenEnv       = "DB_DISPOSABLE_TOKEN"
	publisherTestRole        = "sp_publisher"
	runtimeTestRole          = "stage12_app"
	languageImporterTestRole = "sp_language_importer"
)

const nodeExecutable = "node"

func disposableDatabaseConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
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
		cfg.Password = readDisposablePassword(t, path)
	}
	return cfg
}

// Пути задаёт собственный disposable harness, а не внешний запрос.
func readDisposablePassword(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read disposable password")
	}
	return strings.TrimSpace(string(raw))
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func execSQL(t *testing.T, ctx context.Context, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	_, err := tx.ExecContext(ctx, query, args...)
	requireNoError(t, err)
}

func beginRoleTx(t *testing.T, ctx context.Context, db *sql.DB, role string) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	requireNoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	if role != "" {
		execSQL(t, ctx, tx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize())
	}
	return tx
}

func rejectSQL(t *testing.T, ctx context.Context, db *sql.DB, role, query, code string, args ...any) {
	t.Helper()
	tx := beginRoleTx(t, ctx, db, role)
	_, err := tx.ExecContext(ctx, query, args...)
	assertSQLState(t, err, code)
	_ = tx.Rollback()
}

func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != want {
		t.Fatalf("expected SQLSTATE %s; got %v", want, err)
	}
}

// Наблюдаем реальное ожидание блокировки PostgreSQL; timeout только ограничивает сломанный тест.
