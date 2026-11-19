package identity

import (
	"context"
	"database/sql"
	"strings"
)

const roleEditedEvent = "roles.edited"

// RoleGrantKey is the unique identity of a grant within a role.
type RoleGrantKey struct {
	Operation   string `json:"operation"`
	TypeID      string `json:"type_id,omitempty"`
	ObjectScope string `json:"object_scope,omitempty"`
}

func (g RoleGrant) key() RoleGrantKey {
	return RoleGrantKey{g.Operation, g.TypeID, g.ObjectScope}
}

type RoleChanges struct {
	Name   *string
	Upsert []RoleGrant
	Remove []RoleGrantKey
}

// ChangeRole merges one explicit delta into the current policy and audits it atomically.
func (s *AuthService) ChangeRole(ctx context.Context, req AuthRequest, id string, expected int64, change RoleChanges) (Role, error) {
	var out Role
	err := s.authorizedCommand(ctx, req, []string{opRolesEdit}, roleEditedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		plan, err := prepareRoleMutation(ctx, tx, session, id, expected)
		if err != nil {
			return err
		}
		change.Upsert = normalizeRoleInput(RoleInput{Grants: change.Upsert}).Grants
		input, err := mergeRoleChanges(plan.role, change)
		if err != nil {
			return err
		}
		out, err = s.applyRoleEdit(ctx, tx, session, plan, input, func(ctx context.Context, tx *sql.Tx, roleID string, _ []RoleGrant) error {
			return storeRoleChanges(ctx, tx, roleID, change.Remove, change.Upsert)
		})
		return err
	})
	if err != nil {
		return Role{}, err
	}
	return out, nil
}

func mergeRoleChanges(current Role, change RoleChanges) (RoleInput, error) {
	name := current.Name
	if change.Name != nil {
		name = *change.Name
	}
	if len(change.Upsert) > MaxRoleGrants || len(change.Remove) > MaxRoleGrants ||
		change.Name == nil && len(change.Upsert)+len(change.Remove) == 0 {
		return RoleInput{}, ErrValidation
	}
	active := make(map[RoleGrantKey]RoleGrant, len(current.Grants))
	for _, grant := range current.Grants {
		active[grant.key()] = grant
	}
	seen := make(map[RoleGrantKey]bool, len(change.Remove)+len(change.Upsert))
	for _, key := range change.Remove {
		key.TypeID = strings.ToLower(key.TypeID)
		if _, exists := active[key]; !exists {
			return RoleInput{}, ErrValidation
		}
		delete(active, key)
		seen[key] = true
	}
	for _, grant := range change.Upsert {
		key := grant.key()
		if seen[key] {
			return RoleInput{}, ErrValidation
		}
		seen[key] = true
		active[key] = grant
	}
	input := RoleInput{Name: name, Grants: make([]RoleGrant, 0, len(active))}
	for _, grant := range active {
		input.Grants = append(input.Grants, grant)
	}
	return input, nil
}

func storeRoleChanges(ctx context.Context, tx *sql.Tx, roleID string, remove []RoleGrantKey, upsert []RoleGrant) error {
	for _, key := range remove {
		if _, err := deleteRoleGrant(ctx, tx, roleID, key); err != nil {
			return err
		}
	}
	for _, grant := range upsert {
		id, err := deleteRoleGrant(ctx, tx, roleID, grant.key())
		if err != nil {
			return err
		}
		if err := insertRoleGrant(ctx, tx, roleID, id, grant); err != nil {
			return err
		}
	}
	return nil
}

func deleteRoleGrant(ctx context.Context, tx *sql.Tx, roleID string, key RoleGrantKey) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM public.role_grants
WHERE role_id=$1 AND operation=$2 AND type_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid
AND object_scope IS NOT DISTINCT FROM NULLIF($4,'')`, roleID, key.Operation, key.TypeID, key.ObjectScope).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.grant_fields WHERE grant_id=$1`, id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.role_grants WHERE id=$1`, id); err != nil {
		return "", err
	}
	return id, nil
}
