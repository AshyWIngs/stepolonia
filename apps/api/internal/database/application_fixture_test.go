package database

import (
	"context"
	"database/sql"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"os"
	"stepolonia/apps/api/internal/content"
	"testing"
)

type applicationFixture struct {
	runtime *sql.DB
	reader  *content.PublicReader
}

func newApplicationFixture(t *testing.T, _ *sql.DB, cfg *pgx.ConnConfig) applicationFixture {
	t.Helper()
	runtimeCfg := cfg.Copy()
	runtimeCfg.User = runtimeTestRole
	runtimeCfg.Password = readDisposablePassword(t, os.Getenv("STAGE12_TEST_RUNTIME_PASSWORD_FILE"))
	db := applicationDatabase(t, runtimeCfg, "", "content-runtime")
	return applicationFixture{runtime: db, reader: content.NewPublicReader(db)}
}

type httpTestDatabase struct{ *sql.DB }

func (db httpTestDatabase) Ping(ctx context.Context) error { return db.PingContext(ctx) }
func applicationDatabase(t *testing.T, cfg *pgx.ConnConfig, role, name string) *sql.DB {
	t.Helper()
	clone := cfg.Copy()
	clone.RuntimeParams["application_name"] = name
	if role != "" {
		clone.RuntimeParams["role"] = role
	}
	db := Open(clone)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func publicRequest(t *testing.T, ctx context.Context, client *http.Client, target string, status int) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	requireNoError(t, err)
	response, err := client.Do(request)
	requireNoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	requireNoError(t, err)
	if response.StatusCode != status {
		t.Fatalf("GET %s: got %d, want %d; body=%s", target, response.StatusCode, status, body)
	}
	authResponseRequestID(t, response)
	assertAuthNoStore(t, response)
	return body, response.Header
}
