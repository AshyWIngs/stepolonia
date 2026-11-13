// Package database contains one-shot schema and language-catalog tools.
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql schema_contract.json
var migrations embed.FS

func Open(cfg *pgx.ConnConfig) *sql.DB {
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	return db
}

func Provider(db *sql.DB) (*goose.Provider, error) {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, files)
}

// PrepareRoles is explicit cluster bootstrap. Roles deliberately survive down:
// dropping cluster-wide roles in a database migration could affect other DBs.
func PrepareRoles(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range []string{"sp_publisher", "sp_language_importer"} {
		var safe bool
		err := tx.QueryRowContext(ctx, `SELECT NOT (r.rolcanlogin OR r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_database WHERE datdba=r.oid)
 FROM pg_catalog.pg_roles r WHERE r.rolname=$1`, name).Scan(&safe)
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
		} else if err == nil && !safe {
			return fmt.Errorf("роль %s имеет несовместимые права", name)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CheckRuntime отклоняет привилегированную runtime-роль до изменения схемы.
func CheckRuntime(ctx context.Context, db *sql.DB, name string) error {
	if name == "" {
		return fmt.Errorf("RUNTIME_DB_USER обязателен")
	}
	var safe bool
	err := db.QueryRowContext(ctx, `SELECT NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
 AND r.oid <> (SELECT datdba FROM pg_catalog.pg_database WHERE datname=current_database())
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_default_acl d
 CROSS JOIN LATERAL pg_catalog.aclexplode(d.defaclacl) a
 WHERE d.defaclrole=current_user::regrole AND d.defaclnamespace IN (0,'public'::regnamespace::oid)
 AND ((d.defaclobjtype='r' AND a.grantee IN (0,r.oid))
 OR (d.defaclobjtype='f' AND a.grantee=r.oid AND a.privilege_type='EXECUTE')))
 AND NOT pg_catalog.has_database_privilege(r.oid,current_database(),'CREATE,TEMP')
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relowner=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE proowner=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p
 WHERE p.oid=pg_catalog.to_regprocedure('public.lock_languages(text[])')
 AND pg_catalog.has_function_privilege(r.oid,p.oid,'EXECUTE'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE
 pg_catalog.has_schema_privilege(r.oid,oid,'CREATE') AND nspname NOT LIKE 'pg_temp_%' AND nspname NOT LIKE 'pg_toast_temp_%')
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspowner=r.oid
 AND nspname NOT LIKE 'pg_temp_%' AND nspname NOT LIKE 'pg_toast_temp_%')
 FROM pg_catalog.pg_roles r WHERE r.rolname=$1`, name).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("runtime-роль должна быть отдельной непривилегированной ролью")
	}
	return nil
}

// GrantRuntime оставляет runtime только чтение публичной read model без черновиков и истории.
func GrantRuntime(ctx context.Context, db *sql.DB, name string) error {
	if err := CheckRuntime(ctx, db, name); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	role := pgx.Identifier{name}.Sanitize()
	for _, query := range []string{
		"GRANT USAGE ON SCHEMA public TO " + role,
		"REVOKE ALL ON public.languages, public.articles, public.article_draft_translations, public.article_revisions, public.article_revision_translations, public.public_routes FROM " + role,
		"REVOKE ALL ON public.public_languages, public.public_article_translations, public.public_article_routes FROM " + role,
		"GRANT SELECT ON public.public_languages, public.public_article_translations, public.public_article_routes TO " + role,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	var forbidden bool
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE
 nspname NOT LIKE 'pg_temp_%' AND nspname NOT LIKE 'pg_toast_temp_%'
 AND pg_catalog.has_schema_privilege($1,oid,'CREATE')) OR
 pg_catalog.has_database_privilege($1,current_database(),'CREATE,TEMP') OR
 pg_catalog.has_function_privilege($1,'public.lock_languages(text[])','EXECUTE') OR
 EXISTS (SELECT 1 FROM (VALUES
 ('public.languages'),('public.articles'),('public.article_draft_translations'),
 ('public.article_revisions'),('public.article_revision_translations'),('public.public_routes')) AS tables(name)
 WHERE pg_catalog.has_table_privilege($1,tables.name,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR pg_catalog.has_any_column_privilege($1,tables.name,'SELECT,INSERT,UPDATE,REFERENCES')) OR
 EXISTS (SELECT 1 FROM (VALUES ('public.public_languages'),('public.public_article_translations'),('public.public_article_routes')) AS views(name)
 WHERE pg_catalog.has_table_privilege($1,views.name,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR pg_catalog.has_any_column_privilege($1,views.name,'INSERT,UPDATE,REFERENCES'))`, name).Scan(&forbidden)
	if err != nil {
		return err
	}
	if forbidden {
		return fmt.Errorf("runtime-роль наследует несовместимые права; исправьте bootstrap отдельно")
	}
	return tx.Commit()
}
