package database

import (
	"context"
	"database/sql"
	"fmt"
)

// No CMS pool is provisioned yet. Even existing publisher credentials must have
// no direct or inherited access to dormant account, session or Registry state.
func checkCMSPrivileges(ctx context.Context, tx *sql.Tx) error {
	var safe bool
	err := tx.QueryRowContext(ctx, `WITH objects AS (
 SELECT oid,relowner,relacl FROM pg_catalog.pg_class
 WHERE relnamespace='public'::regnamespace AND relname IN
 ('users','roles','user_roles','permission_operations','role_grants','sessions','content_types','content_fields')
 ) SELECT NOT EXISTS (
 SELECT 1 FROM objects c CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
 WHERE a.grantee<>c.relowner
 ) AND NOT EXISTS (
 SELECT 1 FROM objects c JOIN pg_catalog.pg_attribute col ON col.attrelid=c.oid
 CROSS JOIN LATERAL pg_catalog.aclexplode(col.attacl) a WHERE a.grantee<>c.relowner
 ) AND NOT EXISTS (
 SELECT 1 FROM pg_catalog.pg_proc p
 CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
 WHERE p.oid='public.cms_write_mutex()'::regprocedure AND a.grantee<>p.proowner
 )`).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return fmt.Errorf("CMS foundation must remain owner-only until guarded commands are implemented")
	}
	return nil
}
