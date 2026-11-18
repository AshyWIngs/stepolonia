// Command dev-swagger bootstraps an owned disposable public API fixture.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
)

const runtimeRole = "sp_swagger_runtime"

type healthDatabase struct{ *sql.DB }

func (db healthDatabase) Ping(ctx context.Context) error { return db.PingContext(ctx) }

func main() {
	if err := run(); err != nil {
		slog.Error("swagger_failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := pgx.ParseConfig(os.Getenv("DEV_SWAGGER_DATABASE_URL"))
	if err != nil {
		return err
	}
	token := os.Getenv("DEV_SWAGGER_TOKEN")
	if !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(token) || cfg.Database != "swagger_"+token || cfg.Host != "127.0.0.1" || cfg.User != "postgres" {
		return errors.New("expected launcher-owned loopback disposable PostgreSQL")
	}
	owner := stdlib.OpenDB(*cfg)
	if err = bootstrap(ctx, owner, token); err != nil {
		_ = owner.Close()
		return fmt.Errorf("bootstrap: %w", err)
	}
	if err = owner.Close(); err != nil {
		return err
	}
	cfg.User = runtimeRole
	runtime := stdlib.OpenDB(*cfg)
	defer func() { _ = runtime.Close() }()
	if err = runtime.PingContext(ctx); err != nil {
		return err
	}
	_ = os.Unsetenv("DEV_SWAGGER_DATABASE_URL")
	_ = os.Unsetenv("DEV_SWAGGER_TOKEN")
	port := os.Getenv("DEV_SWAGGER_PORT")
	if port == "" {
		port = "8080"
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	server := &http.Server{
		Handler:           platform.HandlerWithDocumentation(healthDatabase{runtime}, content.NewPublicReader(runtime), logger, nil, siteconfig.NewReader(runtime)),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	fmt.Printf("Swagger UI http://127.0.0.1:%s/docs\nPublic fixture language pl, type article/event, slug welcome-to-stepolonia/community-meetup\nHistorical slug welcome-old returns 301. UI lang kk falls back to pl.\nPrivate auth routes are disabled in this public fixture. Ctrl-C removes the owned database.\n", port)
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func bootstrap(ctx context.Context, db *sql.DB, token string) error {
	var empty bool
	if err := db.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace) AND COALESCE(pg_catalog.shobj_description(oid,'pg_database'),'')='' FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&empty); err != nil {
		return err
	}
	if !empty {
		return errors.New("refusing existing or already marked database")
	}
	// Database identity has been checked against the launcher token before any DDL.
	// PostgreSQL quotes its own current database name inside fixed bootstrap SQL.
	if _, err := db.ExecContext(ctx, `DO $bootstrap$ BEGIN
 EXECUTE format('COMMENT ON DATABASE %I IS %L', current_database(), 'stepolonia:swagger:' || substr(current_database(),9));
 EXECUTE format('REVOKE CREATE,TEMP ON DATABASE %I FROM PUBLIC', current_database());
 END $bootstrap$`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "CREATE ROLE "+runtimeRole+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
		return err
	}
	if err := database.PrepareRoles(ctx, db); err != nil {
		return err
	}
	if err := database.Up(ctx, db, runtimeRole); err != nil {
		return err
	}
	if err := database.Seed(ctx, db); err != nil {
		return err
	}
	if err := seedContent(ctx, db, token); err != nil {
		return err
	}
	return seedSite(ctx, db)
}
