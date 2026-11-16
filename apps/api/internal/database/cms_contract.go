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
func checkCMSDefaultPrivileges(ctx context.Context, db *sql.DB) error {
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
		return fmt.Errorf("unsafe default privileges for future CMS objects; startup stopped before DDL")
	}
	return nil
}

// No CMS pool is provisioned yet. Even existing publisher credentials must have
// no direct or inherited access to dormant account, session or Registry state.
func checkCMSPrivileges(ctx context.Context, tx *sql.Tx) error {
	var safe bool
	err := tx.QueryRowContext(ctx, `WITH objects AS (
 SELECT oid,relowner,relacl FROM pg_catalog.pg_class
 WHERE relnamespace='public'::regnamespace AND relname IN
 ('security_state','mfa_totp','recovery_codes','security_audit','grant_fields','users','roles','user_roles','permission_operations','role_grants','sessions','content_types','content_fields')
 ) SELECT NOT EXISTS (
 SELECT 1 FROM objects c CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
 WHERE a.grantee<>c.relowner
 ) AND NOT EXISTS (
 SELECT 1 FROM objects c JOIN pg_catalog.pg_attribute col ON col.attrelid=c.oid
 CROSS JOIN LATERAL pg_catalog.aclexplode(col.attacl) a WHERE a.grantee<>c.relowner
 ) AND NOT EXISTS (
 SELECT 1 FROM pg_catalog.pg_proc p
 CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
 WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('cms_write_mutex','guard_security_state','guard_security_audit','guard_user_activation') AND a.grantee<>p.proowner
 )`).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("CMS foundation must remain owner-only until guarded commands are implemented")
	}
	return nil
}
