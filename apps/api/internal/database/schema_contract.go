package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Canonical definitions describe the complete domain migration set on PostgreSQL 18.
// Compare complete catalog definitions, including literal whitespace and function bodies.
type schemaContract struct {
	PostgresMajor int               `json:"postgres_major"`
	Constraints   map[string]string `json:"constraints"`
	Triggers      map[string]string `json:"triggers"`
	Functions     map[string]string `json:"functions"`
	Views         map[string]string `json:"views"`
	ViewOptions   map[string]string `json:"view_options"`
	Defaults      map[string]string `json:"defaults"`
	Collations    map[string]string `json:"collations"`
	Columns       map[string]string `json:"columns"`
	Indexes       map[string]string `json:"indexes"`
	Comments      map[string]string `json:"comments"`
	Operations    map[string]string `json:"operations"`
	Privileges    map[string]string `json:"privileges"`
}

func checkDefinitions(ctx context.Context, db *sql.DB, installed bool) error {
	raw, err := migrations.ReadFile("schema_contract.json")
	if err != nil {
		return err
	}
	var contract schemaContract
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
		"indexes": contract.Indexes, "comments": contract.Comments, "operations": contract.Operations, "privileges": contract.Privileges,
	}
	if err := checkSchemaMetadata(ctx, tx); err != nil {
		return err
	}
	if err := checkCMSPrivileges(ctx, tx); err != nil {
		return err
	}
	return checkSchemaDefinitions(ctx, tx, expected)
}

func checkSchemaMetadata(ctx context.Context, tx *sql.Tx) error {
	var safe bool
	err := tx.QueryRowContext(ctx, schemaMetadataQuery).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("несовместимые метаданные publishing; запуск остановлен до DDL")
	}
	return nil
}

func checkSchemaDefinitions(ctx context.Context, tx *sql.Tx, expected map[string]map[string]string) error {
	rows, err := tx.QueryContext(ctx, schemaDefinitionsQuery)
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

const schemaMetadataQuery = `SELECT
 NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace
 AND relname NOT LIKE 'goose_db_version%' AND (relowner<>current_user::regrole OR relrowsecurity OR relforcerowsecurity))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid
 WHERE c.relnamespace='public'::regnamespace AND NOT t.tgisinternal AND t.tgenabled<>'O')
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proowner<>current_user::regrole)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c
 CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
 WHERE c.relnamespace='public'::regnamespace AND c.relkind='r' AND c.relname<>'goose_db_version'
 AND a.grantee<>c.relowner AND a.grantee NOT IN ('sp_publisher'::regrole,'sp_language_importer'::regrole))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute col ON col.attrelid=c.oid
 CROSS JOIN LATERAL pg_catalog.aclexplode(col.attacl) a
 WHERE c.relnamespace='public'::regnamespace AND a.grantee<>c.relowner
 AND a.grantee NOT IN ('sp_publisher'::regrole,'sp_language_importer'::regrole))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p
 CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
 WHERE p.pronamespace='public'::regnamespace AND a.grantee<>p.proowner
 AND a.grantee NOT IN ('sp_publisher'::regrole,'sp_language_importer'::regrole))`

const schemaDefinitionsQuery = `WITH tables AS (
 SELECT oid,relname,relowner,relacl FROM pg_catalog.pg_class
 WHERE relnamespace='public'::regnamespace AND relkind='r' AND relname<>'goose_db_version'
 ), views AS (
 SELECT oid,relname,reloptions FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relkind='v'
 )
 SELECT 'columns',c.relname||'.'||a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod)||CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE ' NULL' END
 FROM pg_catalog.pg_attribute a JOIN tables c ON c.oid=a.attrelid WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraints',c.relname||'.'||n.conname,pg_catalog.pg_get_constraintdef(n.oid,false)
 FROM pg_catalog.pg_constraint n JOIN tables c ON c.oid=n.conrelid
 UNION ALL SELECT 'triggers',c.relname||'.'||t.tgname,pg_catalog.pg_get_triggerdef(t.oid,false)
 FROM pg_catalog.pg_trigger t JOIN tables c ON c.oid=t.tgrelid WHERE NOT t.tgisinternal
 UNION ALL SELECT 'functions',p.proname,pg_catalog.pg_get_functiondef(p.oid)
 FROM pg_catalog.pg_proc p WHERE p.pronamespace='public'::regnamespace
 UNION ALL SELECT 'views',relname,pg_catalog.pg_get_viewdef(oid,false) FROM views
 UNION ALL SELECT 'view_options',relname,COALESCE(array_to_string(reloptions,','),'') FROM views
 UNION ALL SELECT 'defaults',c.relname||'.'||a.attname,pg_catalog.pg_get_expr(d.adbin,d.adrelid,false)
 FROM pg_catalog.pg_attribute a JOIN tables c ON c.oid=a.attrelid
 JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'collations',c.relname||'.'||a.attname,n.nspname||'.'||co.collname
 FROM pg_catalog.pg_attribute a JOIN tables c ON c.oid=a.attrelid
 JOIN pg_catalog.pg_collation co ON co.oid=a.attcollation JOIN pg_catalog.pg_namespace n ON n.oid=co.collnamespace
 WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'indexes',c.relname||'.'||i.relname,pg_catalog.pg_get_indexdef(i.oid)
 FROM tables c JOIN pg_catalog.pg_index x ON x.indrelid=c.oid JOIN pg_catalog.pg_class i ON i.oid=x.indexrelid
 UNION ALL SELECT 'comments',c.relname,COALESCE(pg_catalog.obj_description(c.oid,'pg_class'),'') FROM tables c
 UNION ALL SELECT 'comments',c.relname||'.'||a.attname,pg_catalog.col_description(c.oid,a.attnum)
 FROM tables c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
 WHERE a.attnum>0 AND NOT a.attisdropped AND pg_catalog.col_description(c.oid,a.attnum) IS NOT NULL
 UNION ALL SELECT 'comments','function.'||p.proname,pg_catalog.obj_description(p.oid,'pg_proc')
 FROM pg_catalog.pg_proc p WHERE p.pronamespace='public'::regnamespace AND pg_catalog.obj_description(p.oid,'pg_proc') IS NOT NULL
 UNION ALL SELECT 'privileges','table.'||c.relname||'.'||a.grantee::regrole::text||'.'||a.privilege_type,a.is_grantable::text
 FROM tables c CROSS JOIN LATERAL pg_catalog.aclexplode(c.relacl) a WHERE a.grantee<>c.relowner
 UNION ALL SELECT 'privileges','column.'||c.relname||'.'||col.attname||'.'||a.grantee::regrole::text||'.'||a.privilege_type,a.is_grantable::text
 FROM tables c JOIN pg_catalog.pg_attribute col ON col.attrelid=c.oid
 CROSS JOIN LATERAL pg_catalog.aclexplode(col.attacl) a WHERE a.grantee<>c.relowner
 UNION ALL SELECT 'privileges','function.'||p.proname||'.'||a.grantee::regrole::text||'.'||a.privilege_type,a.is_grantable::text
 FROM pg_catalog.pg_proc p CROSS JOIN LATERAL pg_catalog.aclexplode(p.proacl) a WHERE p.pronamespace='public'::regnamespace AND a.grantee<>p.proowner
 UNION ALL SELECT 'operations',key,scope FROM public.permission_operations`
