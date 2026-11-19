package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Default ACLs apply before an object's explicit REVOKE statements. Reject
// configured grants to other principals for this DDL owner's future objects.
// PostgreSQL's global PUBLIC EXECUTE default is retained: migration functions
// explicitly revoke it, and its presence alone is not operator ACL drift.
func checkAdministrationDefaultPrivileges(ctx context.Context, db *sql.DB) error {
	var unsafe bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_default_acl d
 CROSS JOIN LATERAL pg_catalog.aclexplode(d.defaclacl) a
 WHERE d.defaclrole=current_user::regrole
 AND d.defaclnamespace IN (0,'public'::regnamespace::oid)
 AND d.defaclobjtype IN ('r','S','f')
 AND a.grantee<>d.defaclrole
 AND NOT (d.defaclobjtype='f' AND d.defaclnamespace=0
          AND a.grantee=0 AND a.privilege_type='EXECUTE')
 )`).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("unsafe default privileges for future administration objects; startup stopped before DDL")
	}
	return nil
}

// Security tables are isolated from public readers and publisher credentials.
// Exact sp_security privileges are also checked against the catalog contract.
func checkAdministrationPrivileges(ctx context.Context, tx *sql.Tx) error {
	var safe bool
	err := tx.QueryRowContext(ctx, `WITH objects AS (
 SELECT oid,relowner,relacl FROM pg_catalog.pg_class
 WHERE relnamespace='public'::regnamespace AND relname IN
 ('security_state','session_policy','mfa_totp','recovery_codes','security_audit','grant_fields','users','roles','user_roles','permission_operations','role_grants','sessions','recovery_attempts','auth_attempt_buckets','onboarding_tokens','enrollment_attempts','content_item_assignments')
 ) SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_roles r WHERE r.rolname='sp_security'
 AND NOT (r.rolcanlogin OR r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT pg_catalog.has_database_privilege(r.oid,current_database(),'CREATE,TEMP')
 AND NOT pg_catalog.has_schema_privilege(r.oid,'public','CREATE')
 ) AND NOT pg_catalog.pg_has_role('sp_publisher','sp_security','MEMBER')
 AND NOT pg_catalog.pg_has_role('sp_language_importer','sp_security','MEMBER')
 AND NOT EXISTS (
 SELECT 1 FROM objects c CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
 WHERE a.grantee<>c.relowner AND a.grantee<>'sp_security'::regrole
 ) AND NOT EXISTS (
 SELECT 1 FROM objects c JOIN pg_catalog.pg_attribute col ON col.attrelid=c.oid
 CROSS JOIN LATERAL pg_catalog.aclexplode(col.attacl) a WHERE a.grantee<>c.relowner AND a.grantee<>'sp_security'::regrole
 ) AND NOT EXISTS (
 SELECT 1 FROM pg_catalog.pg_proc p
 CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
 WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('guard_security_state','guard_security_audit','guard_user_activation') AND a.grantee<>p.proowner
 )`).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("security objects expose unexpected privileges")
	}
	return nil
}
