package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check() error {
	user, database := os.Getenv("DB_USER"), os.Getenv("POSTGRES_DB")
	if user == "" || database == "" {
		return fmt.Errorf("не заданы DB_USER и POSTGRES_DB")
	}
	password, err := os.ReadFile("/run/secrets/db_app_password")
	if err != nil || strings.TrimSpace(string(password)) == "" {
		return fmt.Errorf("не удалось прочитать пароль runtime-роли")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	query := "SELECT NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole " +
		"AND NOT has_schema_privilege(current_user, 'public', 'CREATE') " +
		"FROM pg_roles WHERE rolname=current_user"
	cmd := exec.CommandContext(ctx, "/usr/lib/postgresql/18/bin/psql",
		"-X", "--no-password", "--host=127.0.0.1", "--username="+user,
		"--dbname="+database, "--set=ON_ERROR_STOP=1", "--tuples-only", "--no-align", "--command="+query)
	// Пароль не попадает в argv, вывод healthcheck или конфигурацию контейнера.
	cmd.Env = append(os.Environ(), "PGPASSWORD="+strings.TrimSpace(string(password)),
		"PGCONNECT_TIMEOUT=2", "PGOPTIONS=-c statement_timeout=1500")
	output, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(output)) != "t" {
		return fmt.Errorf("проверка соединения или прав runtime-роли не пройдена")
	}
	return nil
}
