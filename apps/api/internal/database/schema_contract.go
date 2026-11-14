package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Канонические определения сняты с чистой 00001 на PostgreSQL 18.
// Контракт ограничен publishing: сравниваем текст pg_get_* целиком, сохраняя
// пробелы внутри SQL literals и тел функций. Смена major требует проверки контракта.
type publishingContract struct {
	PostgresMajor int               `json:"postgres_major"`
	Constraints   map[string]string `json:"constraints"`
	Triggers      map[string]string `json:"triggers"`
	Functions     map[string]string `json:"functions"`
	Views         map[string]string `json:"views"`
	ViewOptions   map[string]string `json:"view_options"`
	Defaults      map[string]string `json:"defaults"`
	Collations    map[string]string `json:"collations"`
	Columns       map[string]string `json:"columns"`
}

func checkDefinitions(ctx context.Context, db *sql.DB, installed bool) error {
	raw, err := migrations.ReadFile("schema_contract.json")
	if err != nil {
		return err
	}
	var contract publishingContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Явная схема в каноническом SQL не зависит от search_path оператора.
	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path=pg_catalog"); err != nil {
		return err
	}
	var major int
	if err := tx.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int/10000").Scan(&major); err != nil {
		return err
	}
	if major != contract.PostgresMajor {
		return fmt.Errorf("контракт publishing требует PostgreSQL %d", contract.PostgresMajor)
	}
	if !installed {
		return nil
	}
	expected := map[string]map[string]string{
		"constraints": contract.Constraints, "triggers": contract.Triggers, "functions": contract.Functions,
		"defaults": contract.Defaults, "collations": contract.Collations, "columns": contract.Columns,
		"views": contract.Views, "view_options": contract.ViewOptions,
	}
	if err := checkPublishingMetadata(ctx, tx); err != nil {
		return err
	}
	return checkPublishingDefinitions(ctx, tx, expected)
}

func checkPublishingMetadata(ctx context.Context, tx *sql.Tx) error {
	var safe bool
	err := tx.QueryRowContext(ctx, publishingMetadataQuery).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("несовместимые метаданные publishing; запуск остановлен до DDL")
	}
	return nil
}

func checkPublishingDefinitions(ctx context.Context, tx *sql.Tx, expected map[string]map[string]string) error {
	rows, err := tx.QueryContext(ctx, publishingDefinitionsQuery)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var category, name, definition string
		if err := rows.Scan(&category, &name, &definition); err != nil {
			return err
		}
		want, found := expected[category][name]
		if !found || strings.TrimSpace(definition) != strings.TrimSpace(want) {
			return fmt.Errorf("дрейф publishing %s %s; запуск остановлен до DDL", category, name)
		}
		delete(expected[category], name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for category, definitions := range expected {
		for name := range definitions {
			return fmt.Errorf("отсутствует publishing %s %s; запуск остановлен до DDL", category, name)
		}
	}
	return nil
}

const publishingMetadataQuery = `SELECT
     (SELECT count(*)=9 AND bool_and(relowner=current_user::regrole AND
      relkind=CASE WHEN relname IN ('public_languages','public_article_translations','public_article_routes') THEN 'v'::"char" ELSE 'r'::"char" END)
      FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
      ('languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes','public_languages','public_article_translations','public_article_routes'))
     AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid
      WHERE c.relnamespace='public'::regnamespace AND c.relname IN
      ('languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes') AND NOT t.tgisinternal AND t.tgenabled<>'O')
     AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname IN
      ('reset_draft_ready','guard_revision_translation','guard_revision','require_closed_revision','guard_published_revision','keep_route_reservation','lock_languages') AND proowner<>current_user::regrole)
     AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
      WHERE p.oid='public.lock_languages(text[])'::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`

const publishingDefinitionsQuery = `WITH tables AS (
 SELECT oid,relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
 ('languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes')
 ), views AS (
 SELECT oid,relname,reloptions FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace
 AND relname IN ('public_languages','public_article_translations','public_article_routes')
 )
 SELECT 'columns',c.relname||'.'||a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod)||CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE ' NULL' END
 FROM pg_catalog.pg_attribute a JOIN tables c ON c.oid=a.attrelid WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraints',c.relname||'.'||n.conname,pg_catalog.pg_get_constraintdef(n.oid,false)
 FROM pg_catalog.pg_constraint n JOIN tables c ON c.oid=n.conrelid
 UNION ALL SELECT 'triggers',c.relname||'.'||t.tgname,pg_catalog.pg_get_triggerdef(t.oid,false)
 FROM pg_catalog.pg_trigger t JOIN tables c ON c.oid=t.tgrelid WHERE NOT t.tgisinternal
 UNION ALL SELECT 'functions',p.proname,pg_catalog.pg_get_functiondef(p.oid)
 FROM pg_catalog.pg_proc p WHERE p.pronamespace='public'::regnamespace AND p.proname IN
 ('reset_draft_ready','guard_revision_translation','guard_revision','require_closed_revision','guard_published_revision','keep_route_reservation','lock_languages')
 UNION ALL SELECT 'views',relname,pg_catalog.pg_get_viewdef(oid,false) FROM views
 UNION ALL SELECT 'view_options',relname,COALESCE(array_to_string(reloptions,','),'') FROM views
 UNION ALL SELECT 'defaults',c.relname||'.'||a.attname,pg_catalog.pg_get_expr(d.adbin,d.adrelid,false)
 FROM pg_catalog.pg_attribute a JOIN tables c ON c.oid=a.attrelid
 JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'collations',c.relname||'.'||a.attname,n.nspname||'.'||co.collname
 FROM pg_catalog.pg_attribute a JOIN tables c ON c.oid=a.attrelid
 JOIN pg_catalog.pg_collation co ON co.oid=a.attcollation JOIN pg_catalog.pg_namespace n ON n.oid=co.collnamespace
 WHERE a.attnum>0 AND NOT a.attisdropped`
