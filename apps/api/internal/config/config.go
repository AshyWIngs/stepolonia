package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseConfig keeps credentials out of connection strings and logs.
func DatabaseConfig() (*pgxpool.Config, error) {
	database := os.Getenv("DB_NAME")
	user := os.Getenv("DB_USER")
	if database == "" || user == "" {
		return nil, fmt.Errorf("DB_NAME and DB_USER are required")
	}
	path := os.Getenv("DB_PASSWORD_FILE")
	if path == "" {
		return nil, fmt.Errorf("DB_PASSWORD_FILE is required")
	}
	password, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read database password file: %w", err)
	}
	if strings.TrimSpace(string(password)) == "" {
		return nil, fmt.Errorf("database password is empty")
	}
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	cfg.ConnConfig.Host = "postgres"
	cfg.ConnConfig.Port = 5432
	cfg.ConnConfig.Database = database
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = strings.TrimSpace(string(password))
	cfg.ConnConfig.TLSConfig = nil // Local, isolated Compose data network; no public DB port.
	cfg.ConnConfig.Fallbacks = nil
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "sp-api"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "3000"
	cfg.MaxConns = 5
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	return cfg, nil
}
