// Package testpostgres guards PostgreSQL resources used by integration tests.
package testpostgres

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// AdminConfig requires the harness-owned loopback-only disposable administration database.
func AdminConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	dsn := os.Getenv("STAGE31_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT RUN: STAGE31_TEST_DATABASE_URL is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid disposable database configuration")
	}
	if !strings.HasPrefix(cfg.Database, "stage31_") || (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") {
		t.Fatal("refusing database outside loopback stage31_ scope")
	}
	return cfg
}
