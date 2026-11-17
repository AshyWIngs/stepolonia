package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
)

// CheckSecurity verifies the dedicated authentication login and effective privileges.
func CheckSecurity(ctx context.Context, db *sql.DB) error {
	connection, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	var login string
	if err = connection.Raw(func(driver any) error {
		postgres, ok := driver.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("authentication pool requires the PostgreSQL driver")
		}
		login = postgres.Conn().Config().User
		return nil
	}); err != nil {
		return err
	}
	if err = checkSecurityConnection(ctx, connection, login); err != nil {
		return err
	}
	if err = connection.Close(); err != nil {
		return err
	}
	return checkSecurityDefinitions(ctx, db)
}

func checkSecurityConnection(ctx context.Context, connection *sql.Conn, login string) error {
	var safe bool
	err := connection.QueryRowContext(ctx, `WITH effective AS (
 SELECT c.oid,c.relname,p.privilege
 FROM pg_catalog.pg_class c
 CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) p(privilege)
 WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','v')
 ) SELECT
 current_user=session_user AND session_user=$1 AND r.rolcanlogin AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
 AND r.oid<>(SELECT datdba FROM pg_catalog.pg_database WHERE datname=current_database())
 AND pg_catalog.pg_has_role(r.oid,'sp_security','USAGE')
 AND EXISTS (SELECT 1 FROM pg_catalog.pg_roles s WHERE s.rolname='sp_security'
 AND NOT (s.rolcanlogin OR s.rolsuper OR s.rolcreatedb OR s.rolcreaterole OR s.rolreplication OR s.rolbypassrls))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles p WHERE p.oid<>r.oid
 AND p.rolname<>'sp_security' AND pg_catalog.pg_has_role(r.oid,p.oid,'MEMBER'))
 AND NOT pg_catalog.has_database_privilege(r.oid,current_database(),'CREATE,TEMP')
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n
 WHERE n.nspowner=r.oid OR pg_catalog.has_schema_privilege(r.oid,n.oid,'CREATE'))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relowner=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE proowner=r.oid)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace='public'::regnamespace
 AND pg_catalog.has_function_privilege(r.oid,p.oid,'EXECUTE'))
 AND pg_catalog.has_schema_privilege(r.oid,'public','USAGE')
 AND NOT EXISTS (SELECT 1 FROM effective e WHERE
 (pg_catalog.has_table_privilege(r.oid,e.oid,e.privilege)
 OR (e.privilege IN ('SELECT','INSERT','UPDATE','REFERENCES')
 AND pg_catalog.has_any_column_privilege(r.oid,e.oid,e.privilege))) <>
 CASE e.privilege
 WHEN 'SELECT' THEN e.relname IN ('users','roles','user_roles','permission_operations','role_grants',
 'grant_fields','security_state','mfa_totp','recovery_codes','sessions','recovery_attempts','auth_attempts','onboarding_tokens','enrollment_attempts')
 WHEN 'INSERT' THEN e.relname IN ('sessions','recovery_attempts','auth_attempts','recovery_codes','security_audit','users','mfa_totp','onboarding_tokens','enrollment_attempts')
 WHEN 'UPDATE' THEN e.relname IN ('users','mfa_totp','recovery_codes','sessions','recovery_attempts','auth_attempts','onboarding_tokens','enrollment_attempts')
 WHEN 'DELETE' THEN e.relname IN ('auth_attempts','sessions')
 ELSE false END)
 FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`, login).Scan(&safe)
	if err != nil {
		return fmt.Errorf("check authentication pool privileges: %w", err)
	}
	if !safe {
		return fmt.Errorf("authentication pool requires an isolated login inheriting only sp_security")
	}
	return nil
}

func checkSecurityDefinitions(ctx context.Context, db *sql.DB) error {
	contract, err := readSchemaContract()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path=pg_catalog"); err != nil {
		return err
	}
	var safe bool
	err = tx.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int/10000=$1
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace='public'::regnamespace
 AND (c.relowner<>(SELECT relowner FROM pg_catalog.pg_class WHERE oid='public.security_state'::regclass)
 OR c.relowner=current_user::regrole OR c.relrowsecurity OR c.relforcerowsecurity))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace='public'::regnamespace
 AND p.proowner<>(SELECT relowner FROM pg_catalog.pg_class WHERE oid='public.security_state'::regclass))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid
 WHERE c.relnamespace='public'::regnamespace AND NOT t.tgisinternal AND t.tgenabled<>'O')`, contract.PostgresMajor).Scan(&safe)
	if err != nil {
		return fmt.Errorf("check authentication schema metadata: %w", err)
	}
	if !safe {
		return fmt.Errorf("authentication schema ownership or guards drifted")
	}
	return checkSchemaDefinitions(ctx, tx, contract.definitions())
}
