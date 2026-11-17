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
		var conflict bool
		err := db.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
 ('article_draft_translations','article_drafts_pkey','article_revision_translations','article_revision_translations_pkey','article_revisions','article_revisions_number_key','article_revisions_owner_key','article_revisions_pkey','articles','articles_pkey','content_fields','content_fields_pkey','content_fields_type_key','content_types','content_types_key_key','content_types_pkey','grant_fields','grant_fields_field_idx','grant_fields_pkey','languages','languages_pkey','mfa_totp','mfa_totp_pkey','permission_operations','permission_operations_pkey','public_article_routes','public_article_translations','public_languages','public_routes','public_routes_owner_key','public_routes_pkey','public_site','public_ui_languages','public_ui_messages','recovery_codes','recovery_codes_digest_key','recovery_codes_pkey','role_grants','role_grants_id_type_key','role_grants_operation_idx','role_grants_operation_key','role_grants_pkey','role_grants_type_idx','roles','roles_name_key_key','roles_pkey','security_audit','security_audit_pkey','security_state','security_state_pkey','sessions','sessions_pkey','sessions_token_digest_key','sessions_user_idx','site_revision_keys','site_revision_keys_pkey','site_revision_languages','site_revision_languages_pkey','site_revision_messages','site_revision_messages_pkey','site_revisions','site_revisions_pkey','site_state','site_state_pkey','ui_message_keys','ui_message_keys_pkey','user_roles','user_roles_pkey','user_roles_role_idx','users','users_login_key_key','users_pkey')) OR
 EXISTS (SELECT 1 FROM pg_catalog.pg_type WHERE typnamespace='public'::regnamespace AND typname IN
 ('article_draft_translations','article_revision_translations','article_revisions','articles','content_fields','content_types','grant_fields','languages','mfa_totp','permission_operations','public_article_routes','public_article_translations','public_languages','public_routes','public_site','public_ui_languages','public_ui_messages','recovery_codes','role_grants','roles','security_audit','security_state','sessions','site_revision_keys','site_revision_languages','site_revision_messages','site_revisions','site_state','ui_message_keys','user_roles','users')) OR
 EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname IN
 ('guard_security_state','guard_security_audit','guard_user_activation','cms_write_mutex','reset_draft_ready','guard_revision_translation','guard_revision','require_closed_revision','guard_published_revision','keep_route_reservation','lock_languages','ui_placeholders','guard_site_child','guard_site_revision','guard_site_state'))`).Scan(&conflict)
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
