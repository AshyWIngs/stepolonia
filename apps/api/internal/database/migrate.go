package database

import (
	"context"
	"database/sql"
	"fmt"
	"stepolonia/apps/api/internal/content"
)

// Up проверяет известную структуру до goose и обновляет публичные права после миграций.
// Повторный запуск также проверяет дрейф, хотя goose уже не выполняет SQL миграций.
func Up(ctx context.Context, db *sql.DB, runtime string) error {
	if err := CheckRuntime(ctx, db, runtime); err != nil {
		return err
	}
	if err := CheckSchema(ctx, db); err != nil {
		return err
	}
	p, err := Provider(db)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return err
	}
	if err := CheckSchema(ctx, db); err != nil {
		return err
	}
	if err := content.SeedInitialRegistry(ctx, db); err != nil {
		return err
	}
	return GrantRuntime(ctx, db, runtime)
}

// CheckSchema validates the complete initial domain set without repairing drift.
func CheckSchema(ctx context.Context, db *sql.DB) error {
	if err := checkCMSDefaultPrivileges(ctx, db); err != nil {
		return err
	}
	version, err := readMigrationVersion(ctx, db)
	if err != nil {
		return err
	}
	if version == 0 {
		if err := checkFreshSchema(ctx, db); err != nil {
			return err
		}
		return checkDefinitions(ctx, db, false)
	}
	return checkDefinitions(ctx, db, true)
}

// Down допускает откат только в явно помеченной одноразовой БД.
// Токен передаётся вызывающим кодом из окружения, а не из аргументов процесса.
func Down(ctx context.Context, db *sql.DB, token string) error {
	if token == "" {
		return fmt.Errorf("для down требуется DB_DISPOSABLE_TOKEN одноразовой БД")
	}
	var disposable bool
	err := db.QueryRowContext(ctx, `SELECT left(current_database(),8)='stage12_'
 AND pg_catalog.shobj_description(oid,'pg_database')=$1
 FROM pg_catalog.pg_database WHERE datname=current_database()`, "stepolonia:disposable:"+token).Scan(&disposable)
	if err != nil {
		return err
	}
	if !disposable {
		return fmt.Errorf("down разрешён только для помеченной одноразовой БД stage12_")
	}
	if err := CheckSchema(ctx, db); err != nil {
		return err
	}
	version, err := readMigrationVersion(ctx, db)
	if err != nil {
		return err
	}
	if version == 0 {
		return nil
	}
	p, err := Provider(db)
	if err != nil {
		return err
	}
	_, err = p.DownTo(ctx, 0)
	return err
}
