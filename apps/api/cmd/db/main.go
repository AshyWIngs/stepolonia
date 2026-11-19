package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"stepolonia/apps/api/internal/config"
	"stepolonia/apps/api/internal/database"
)

const (
	commandImportLanguages = "import-languages"
	commandBootstrapAdmin  = "bootstrap-admin"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := 2 * time.Minute
	if len(os.Args) > 1 && os.Args[1] == commandBootstrapAdmin {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		// Connection and query errors can include SQL and connection parameters.
		// Print only the SQLSTATE for PostgreSQL errors.
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) {
			fmt.Fprintln(os.Stderr, "Database operation rejected. SQLSTATE="+pgerr.Code)
		} else {
			fmt.Fprintln(os.Stderr, "Operation failed:", safeError(err))
		}
		os.Exit(1)
	}
}

type inputError struct{ message string }

func (e inputError) Error() string { return e.message }
func safeError(err error) string {
	var input inputError
	if errors.As(err, &input) {
		return input.message
	}
	return "check the selected database settings and availability"
}

func run(ctx context.Context, args []string) error {
	if err := validateCommand(args); err != nil {
		return err
	}
	if args[0] == commandBootstrapAdmin {
		return runBootstrap(ctx)
	}
	var rows []database.Language
	if args[0] == commandImportLanguages {
		// Validate the entire CSV before opening a database connection.
		var err error
		rows, err = readLanguageFile(args[1])
		if err != nil {
			return err
		}
	}
	cfg, err := toolConfig()
	if err != nil {
		return err
	}
	db := database.Open(cfg)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "prepare-roles":
		return database.PrepareRoles(ctx, db)
	case commandImportLanguages:
		return database.ImportLanguages(ctx, db, rows)
	case commandInitializeSite:
		return initializeSite(ctx, db, args[1], args[2])
	case "migrate":
		return runMigration(ctx, db, args[1])
	}
	return nil
}

func validateCommand(args []string) error {
	if len(args) == 0 {
		return inputError{"commands: prepare-roles | migrate up|down|status | import-languages FILE | bootstrap-admin | initialize-site TITLE LANGUAGE"}
	}
	switch args[0] {
	case "prepare-roles", commandBootstrapAdmin:
		if len(args) == 1 {
			return nil
		}
	case commandInitializeSite:
		if len(args) == 3 {
			return nil
		}
	case commandImportLanguages:
		if len(args) == 2 {
			return nil
		}
	case "migrate":
		if len(args) != 2 {
			break
		}
		action := args[len(args)-1]
		if action == "up" || action == "down" || action == "status" {
			return nil
		}
	}
	return inputError{"invalid db command"}
}

func readLanguageFile(path string) ([]database.Language, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, inputError{"could not open CSV"}
	}
	defer f.Close()
	rows, err := database.ReadLanguages(f)
	if err != nil {
		return nil, inputError{err.Error()}
	}
	return rows, nil
}

func runMigration(ctx context.Context, db *sql.DB, action string) error {
	switch action {
	case "up":
		return database.Up(ctx, db, os.Getenv("RUNTIME_DB_USER"))
	case "down":
		token := os.Getenv("DB_DISPOSABLE_TOKEN")
		if token == "" {
			return inputError{"down requires DB_DISPOSABLE_TOKEN for the disposable database"}
		}
		return database.Down(ctx, db, token)
	case "status":
		return printMigrationStatus(ctx, db)
	}
	return nil
}

func printMigrationStatus(ctx context.Context, db *sql.DB) error {
	p, err := database.Provider(db)
	if err != nil {
		return err
	}
	status, err := p.Status(ctx)
	if err != nil {
		return err
	}
	for _, s := range status {
		fmt.Printf("%d %s\n", s.Source.Version, s.State)
	}
	return nil
}

func toolConfig() (*pgx.ConnConfig, error) {
	cfg, err := config.DatabaseConfig()
	if err != nil {
		return nil, err
	}
	if host := os.Getenv("DB_HOST"); host != "" {
		cfg.ConnConfig.Host = host
	}
	if port := os.Getenv("DB_PORT"); port != "" {
		p, err := strconv.ParseUint(port, 10, 16)
		if err != nil || p == 0 {
			return nil, inputError{"invalid DB_PORT"}
		}
		cfg.ConnConfig.Port = uint16(p)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "sp-db-tools"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "60000"
	return cfg.ConnConfig, nil
}
