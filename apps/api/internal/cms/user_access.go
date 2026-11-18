package cms

import (
	"context"
	"database/sql"
	"slices"
)

const (
	maxUsers = 100
	maxRoles = 32
)

// MaxUserRoles bounds the complete role membership set of one user.
const MaxUserRoles = 16

// MaxRoleGrants bounds the complete explicit policy replacement for one role.
const MaxRoleGrants = 64

const (
	grantScopeContent      = "content"
	grantScopeInstallation = "installation"
)

const (
	objectScopeAny      = "any"
	objectScopeType     = "type"
	objectScopeAssigned = "assigned"
)

const (
	authorityTargetInstallation = "installation"
	authorityTargetType         = "type"
)

type accessGrant struct {
	operation, scope, typeID, objectScope string
	delegable                             bool
	targetScope, ceiling                  string
	mayDelegate                           bool
	operations, fields                    []string
}

type userAccess struct {
	active, latent []accessGrant
}

type accessSnapshot map[string]userAccess

// Counts are checked before loading an affected set. Exceeding a cap never
// silently removes a role, grant or recipient from an authorization decision.
func checkAccessCaps(ctx context.Context, tx *sql.Tx) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM public.users)<=$1 AND
 (SELECT count(*) FROM public.roles)<=$2 AND
 NOT EXISTS(SELECT 1 FROM public.role_grants GROUP BY role_id HAVING count(*)>$3) AND
 NOT EXISTS(SELECT 1 FROM public.user_roles GROUP BY user_id HAVING count(*)>$4)`, maxUsers, maxRoles, MaxRoleGrants, MaxUserRoles).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrSecurityUnavailable
	}
	return checkBootstrapOperations(ctx, tx)
}

func loadAccess(ctx context.Context, tx *sql.Tx) (accessSnapshot, error) {
	roles, err := boundedRolePolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	return membershipAccess(ctx, tx, roles)
}

func boundedRolePolicies(ctx context.Context, tx *sql.Tx) (map[string][]accessGrant, error) {
	if err := checkAccessCaps(ctx, tx); err != nil {
		return nil, err
	}
	// Decode each role once. Memberships share immutable field/operation slices,
	// rather than multiplying potentially large policy sets by every recipient.
	return loadRolePolicies(ctx, tx)
}

func membershipAccess(ctx context.Context, tx *sql.Tx, roles map[string][]accessGrant) (accessSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ur.user_id::text,ur.role_id::text,r.state='active' FROM public.user_roles ur JOIN public.roles r ON r.id=ur.role_id ORDER BY ur.user_id,r.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(accessSnapshot)
	for rows.Next() {
		var user, role string
		var active bool
		if err = rows.Scan(&user, &role, &active); err != nil {
			return nil, err
		}
		access := out[user]
		access.latent = append(access.latent, roles[role]...)
		if active {
			access.active = append(access.active, roles[role]...)
		}
		out[user] = access
	}
	return out, rows.Err()
}

func loadRolePolicies(ctx context.Context, tx *sql.Tx) (map[string][]accessGrant, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM public.roles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	out := make(map[string][]accessGrant, len(ids))
	for _, id := range ids {
		grants, err := queryRoleGrants(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out[id] = accessGrants(grants)
	}
	return out, nil
}

func coversFields(source, requested []string) bool {
	// Small operation/field sets avoid an allocation. Large policy field sets
	// use linear-time membership checks while the security mutex is held.
	const linearFieldSetLimit = 32
	if len(source) > linearFieldSetLimit && len(requested) > 1 {
		available := make(map[string]struct{}, len(source))
		for _, field := range source {
			available[field] = struct{}{}
		}
		for _, field := range requested {
			if _, ok := available[field]; !ok {
				return false
			}
		}
		return true
	}
	for _, field := range requested {
		if !slices.Contains(source, field) {
			return false
		}
	}
	return true
}

func coversObject(source, requested string) bool {
	return source == requested || source == objectScopeAny && requested == objectScopeAssigned
}

func (source accessGrant) coversContent(target accessGrant) bool {
	if source.operation == opContentDelegate {
		return slices.Contains(source.operations, target.operation) &&
			(target.objectScope == objectScopeType || coversObject(source.ceiling, target.objectScope)) &&
			(!target.delegable || source.mayDelegate) &&
			(source.targetScope == authorityTargetInstallation || source.typeID == target.typeID && coversFields(source.fields, target.fields))
	}
	return source.delegable && source.operation == target.operation &&
		source.typeID == target.typeID && coversObject(source.objectScope, target.objectScope) && coversFields(source.fields, target.fields)
}

func (source accessGrant) coversSecurity(target accessGrant) bool {
	if !source.delegable || source.operation != target.operation {
		return false
	}
	if target.operation != opContentDelegate {
		return true
	}
	return coversFields(source.operations, target.operations) &&
		(!target.mayDelegate || source.mayDelegate) && coversObject(source.ceiling, target.ceiling) &&
		(source.targetScope == authorityTargetInstallation || source.targetScope == target.targetScope && source.typeID == target.typeID && coversFields(source.fields, target.fields))
}

func withinEnvelope(actor, target []accessGrant) bool {
	for _, requested := range target {
		covered := false
		for _, source := range actor {
			if requested.scope == grantScopeContent {
				covered = source.coversContent(requested)
			} else {
				covered = source.coversSecurity(requested)
			}
			if covered {
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}
