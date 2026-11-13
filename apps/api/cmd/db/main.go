package main

import (
	"context"
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

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		// Connection/query errors can contain SQL or connection details. Keep them
		// private; SQLSTATE is enough to distinguish an operator failure safely.
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
	if len(args) == 0 {
		return inputError{"команды: prepare-roles | migrate up|down|status | seed | import-languages FILE"}
	}
	valid := len(args) == 1 && (args[0] == "prepare-roles" || args[0] == "seed") || len(args) == 2 && (args[0] == "import-languages" || args[0] == "migrate" && (args[1] == "up" || args[1] == "down" || args[1] == "status"))
	if !valid {
		return inputError{"неверная команда db"}
	}
	var rows []database.Language
	if args[0] == "import-languages" {
		f, err := os.Open(args[1])
		if err != nil {
			return inputError{"не удалось открыть CSV"}
		}
		defer f.Close()
		rows, err = database.ReadLanguages(f)
		if err != nil {
			return inputError{err.Error()}
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
	case "import-languages":
		return database.ImportLanguages(ctx, db, rows)
	case "migrate":
		switch args[1] {
		case "up":
			return database.Up(ctx, db, os.Getenv("RUNTIME_DB_USER"))
		case "down":
			token := os.Getenv("DB_DISPOSABLE_TOKEN")
			if token == "" {
				return inputError{"для down требуется DB_DISPOSABLE_TOKEN одноразовой БД"}
			}
			return database.Down(ctx, db, token)
		case "status":
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
