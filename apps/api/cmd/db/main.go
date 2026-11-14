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

const commandImportLanguages = "import-languages"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		// Ошибки соединения и запросов могут содержать SQL и параметры подключения.
		// Для диагностики выводим SQLSTATE, сохраняя остальные подробности закрытыми.
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) {
			fmt.Fprintln(os.Stderr, "Операция БД отклонена; SQLSTATE="+pgerr.Code)
		} else {
			fmt.Fprintln(os.Stderr, "Операция не выполнена:", safeError(err))
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
	return "проверьте параметры и доступность выбранной БД"
}

func run(ctx context.Context, args []string) error {
	if err := validateCommand(args); err != nil {
		return err
	}
	var rows []database.Language
	if args[0] == commandImportLanguages {
		// Проверяем весь CSV до подключения к БД и любых изменений данных.
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
	case "seed":
		return database.Seed(ctx, db)
	case commandImportLanguages:
		return database.ImportLanguages(ctx, db, rows)
	case "migrate":
		return runMigration(ctx, db, args[1])
	}
	return nil
}

func validateCommand(args []string) error {
	if len(args) == 0 {
		return inputError{"команды: prepare-roles | migrate up|down|status | seed | import-languages FILE"}
	}
	switch args[0] {
	case "prepare-roles", "seed":
		if len(args) == 1 {
			return nil
		}
	case commandImportLanguages:
		if len(args) == 2 {
			return nil
		}
	case "migrate":
		if len(args) == 2 && (args[1] == "up" || args[1] == "down" || args[1] == "status") {
			return nil
		}
	}
	return inputError{"неверная команда db"}
}

func readLanguageFile(path string) ([]database.Language, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, inputError{"не удалось открыть CSV"}
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
			return inputError{"для down требуется DB_DISPOSABLE_TOKEN одноразовой БД"}
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
			return nil, inputError{"некорректный DB_PORT"}
		}
		cfg.ConnConfig.Port = uint16(p)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "sp-db-tools"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "60000"
	return cfg.ConnConfig, nil
}
