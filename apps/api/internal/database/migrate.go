package database

import (
	"context"
	"database/sql"
	"fmt"
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
	return GrantRuntime(ctx, db, runtime)
}

// CheckSchema ограниченно проверяет publishing, не пытаясь исправлять чужую схему.
func CheckSchema(ctx context.Context, db *sql.DB) error {
	var history bool
	if err := db.QueryRowContext(ctx, "SELECT pg_catalog.to_regclass('public.goose_db_version') IS NOT NULL").Scan(&history); err != nil {
		return err
	}
	var version int64
	if history {
		if err := db.QueryRowContext(ctx, "SELECT COALESCE(max(version_id),0) FROM public.goose_db_version WHERE is_applied").Scan(&version); err != nil {
			return err
		}
	}
	if version != 0 && version != 1 {
		return fmt.Errorf("неподдерживаемая версия publishing")
	}
	if version == 0 {
		var conflict bool
		err := db.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
 ('languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes','public_languages','public_article_translations','public_article_routes')) OR
 EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname IN
 ('reset_draft_ready','guard_revision_translation','guard_revision','require_closed_revision','guard_published_revision','keep_route_reservation','lock_languages'))`).Scan(&conflict)
		if err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("publishing-объекты без истории миграций; запуск остановлен до DDL")
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
	p, err := Provider(db)
	if err != nil {
		return err
	}
	_, err = p.Down(ctx)
	return err
}
