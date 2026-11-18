package cms

import (
	"context"
	"database/sql"
	"fmt"
)

const seedAdminName = "Site Admin"

var bootstrapContentOperations = []string{opContentCreate, string(OpContentRead), string(OpContentUpdate), opContentReady, opContentPublish, "content.items.unpublish", "content.items.assign"}
var bootstrapInstallationOperations = []string{string(OpContentTypesManage), opContentInitializeAccess, opContentDelegate, "users.create", "users.read", "users.activate", "users.disable", "roles.create", "roles.read", "roles.edit", "roles.assign", "roles.disable", "roles.enable", "roles.archive", "roles.restore", "roles.delete", "sessions.revoke_any", "audit.read", opSettingsManage}

func seedBootstrapRoles(ctx context.Context, tx *sql.Tx) error {
	if err := checkBootstrapOperations(ctx, tx); err != nil {
		return err
	}
	var articleTypeID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM public.content_types WHERE key='article'`).Scan(&articleTypeID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	for _, roleName := range []string{"Author", "Editor", seedAdminName} {
		if err := seedBootstrapRole(ctx, tx, roleName, articleTypeID); err != nil {
			return err
		}
	}
	return nil
}

func checkBootstrapAdmin(ctx context.Context, tx *sql.Tx, user string) error {
	for _, op := range bootstrapInstallationOperations {
		var allowed bool
		query := `SELECT EXISTS(SELECT 1 FROM public.user_roles ur JOIN public.roles r ON r.id=ur.role_id JOIN public.role_grants g ON g.role_id=r.id WHERE ur.user_id=$1 AND r.state='active' AND g.operation=$2 AND g.scope='installation' AND g.can_delegate)`
		if op == opContentDelegate {
			query = `SELECT EXISTS(SELECT 1 FROM public.user_roles ur JOIN public.roles r ON r.id=ur.role_id JOIN public.role_grants g ON g.role_id=r.id WHERE ur.user_id=$1 AND r.state='active' AND g.operation=$2 AND g.scope='installation' AND g.can_delegate AND g.target_scope='installation' AND g.object_ceiling='any' AND g.may_delegate_content AND g.delegate_operations @> $3::text[])`
			if err := tx.QueryRowContext(ctx, query, user, op, bootstrapContentOperations).Scan(&allowed); err != nil {
				return err
			}
		} else if err := tx.QueryRowContext(ctx, query, user, op).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("bootstrap admin grants incomplete")
		}
	}
	return nil
}

func checkPendingBootstrap(ctx context.Context, tx *sql.Tx, user string) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM public.users)=1 AND EXISTS(SELECT 1 FROM public.users WHERE id=$1 AND state='pending' AND enrollment_completed_at IS NULL) AND (SELECT count(*) FROM public.roles)=3 AND NOT EXISTS(SELECT 1 FROM public.roles WHERE state<>'active' OR name_key NOT IN ('Author','Editor','Site Admin')) AND NOT EXISTS(SELECT 1 FROM public.user_roles) AND NOT EXISTS(SELECT 1 FROM public.sessions) AND NOT EXISTS(SELECT 1 FROM public.recovery_codes) AND (SELECT count(*) FROM public.mfa_totp)=1 AND EXISTS(SELECT 1 FROM public.mfa_totp WHERE user_id=$1 AND confirmed_at IS NULL AND last_counter=-1)`, user).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("pending bootstrap security state changed")
	}
	return nil
}

func checkBootstrapOperations(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT key,scope FROM public.permission_operations ORDER BY key`)
	if err != nil {
		return err
	}
	expected := map[string]string{}
	for _, op := range bootstrapContentOperations {
		expected[op] = "content"
	}
	for _, op := range bootstrapInstallationOperations {
		expected[op] = "installation"
	}
	for rows.Next() {
		var op, scope string
		if err := rows.Scan(&op, &scope); err != nil {
			_ = rows.Close()
			return err
		}
		if expected[op] != scope {
			_ = rows.Close()
			return fmt.Errorf("operation registry drift")
		}
		delete(expected, op)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(expected) != 0 {
		return fmt.Errorf("operation registry incomplete")
	}
	return nil
}

func seedInstallationGrants(ctx context.Context, tx *sql.Tx, roleID string) error {
	for _, op := range bootstrapInstallationOperations {
		id, err := randomID()
		if err != nil {
			return err
		}
		if op == opContentDelegate {
			if _, err := tx.ExecContext(ctx, `INSERT INTO public.role_grants(id,role_id,operation,scope,can_delegate,delegate_operations,target_scope,object_ceiling,may_delegate_content) VALUES($1,$2,$3,'installation',true,$4::text[],'installation','any',true)`, id, roleID, op, bootstrapContentOperations); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO public.role_grants(id,role_id,operation,scope,can_delegate) VALUES($1,$2,$3,'installation',true)`, id, roleID, op); err != nil {
			return err
		}
	}
	return nil
}

func seedContentGrants(ctx context.Context, tx *sql.Tx, roleID, roleName, articleTypeID string) error {
	operations := bootstrapContentOperations
	scope := "any"
	delegable := roleName == seedAdminName
	if roleName == "Author" {
		operations = bootstrapContentOperations[:4]
		scope = objectScopeAssigned
	}
	if roleName == "Editor" {
		operations = bootstrapContentOperations[1:6]
	}
	for _, op := range operations {
		if err := seedContentGrant(ctx, tx, roleID, articleTypeID, op, scope, delegable); err != nil {
			return err
		}
	}
	return nil
}

func seedContentGrant(ctx context.Context, tx *sql.Tx, roleID, articleTypeID, op, scope string, delegable bool) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	objectScope := scope
	if op == opContentCreate {
		objectScope = "type"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope,can_delegate) VALUES($1,$2,$3,'content',$4,$5,$6)`, id, roleID, op, articleTypeID, objectScope, delegable); err != nil {
		return err
	}
	if op == opContentCreate || op == string(OpContentRead) || op == string(OpContentUpdate) || op == opContentReady || op == opContentPublish {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.grant_fields(grant_id,type_id,field_id) SELECT $1,type_id,field_id FROM public.content_fields WHERE type_id=$2`, id, articleTypeID); err != nil {
			return err
		}
	}
	return nil
}

func seedBootstrapRole(ctx context.Context, tx *sql.Tx, roleName string, articleTypeID sql.NullString) error {
	roleID, err := randomID()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.roles(id,name_key,state) VALUES($1,$2,'active')`, roleID, roleName); err != nil {
		return err
	}
	if roleName == seedAdminName {
		if err := seedInstallationGrants(ctx, tx, roleID); err != nil {
			return err
		}
	}
	if !articleTypeID.Valid {
		return nil
	}
	if err := seedContentGrants(ctx, tx, roleID, roleName, articleTypeID.String); err != nil {
		return err
	}
	return nil
}
