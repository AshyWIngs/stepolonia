package cms

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
)

const (
	maxUsers           = 100
	maxRoles           = 32
	maxRoleGrants      = 64
	maxUserMemberships = 16
)

const (
	grantScopeContent      = "content"
	grantScopeInstallation = "installation"
)

const (
	objectScopeAny  = "any"
	objectScopeType = "type"
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
 NOT EXISTS(SELECT 1 FROM public.user_roles GROUP BY user_id HAVING count(*)>$4)`, maxUsers, maxRoles, maxRoleGrants, maxUserMemberships).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrSecurityUnavailable
	}
	return checkBootstrapOperations(ctx, tx)
}

func loadAccess(ctx context.Context, tx *sql.Tx) (accessSnapshot, error) {
	if err := checkAccessCaps(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT ur.user_id::text,r.state='active',g.operation,g.scope,COALESCE(g.type_id::text,''),COALESCE(g.object_scope,''),g.can_delegate,COALESCE(g.target_scope,''),COALESCE(g.object_ceiling,''),g.may_delegate_content,array_to_json(g.delegate_operations)::text,COALESCE((SELECT json_agg(f.field_id::text ORDER BY f.field_id)::text FROM public.grant_fields f WHERE f.grant_id=g.id),'[]') FROM public.user_roles ur JOIN public.roles r ON r.id=ur.role_id JOIN public.role_grants g ON g.role_id=r.id ORDER BY ur.user_id,r.id,g.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(accessSnapshot)
	for rows.Next() {
		var user, ops, fields string
		var active bool
		var g accessGrant
		if err = rows.Scan(&user, &active, &g.operation, &g.scope, &g.typeID, &g.objectScope, &g.delegable, &g.targetScope, &g.ceiling, &g.mayDelegate, &ops, &fields); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(ops), &g.operations); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(fields), &g.fields); err != nil {
			return nil, err
		}
		if err = g.validate(); err != nil {
			return nil, err
		}
		access := out[user]
		access.latent = append(access.latent, g)
		if active {
			access.active = append(access.active, g)
		}
		out[user] = access
	}
	return out, rows.Err()
}

func (g accessGrant) validate() error {
	scope, err := Operation(g.operation).Scope()
	if err != nil {
		return err
	}
	if scope == InstallationScope && g.scope != grantScopeInstallation || scope == ContentScope && g.scope != grantScopeContent {
		return ErrSecurityUnavailable
	}
	if g.operation == opContentDelegate {
		for _, operation := range g.operations {
			kind, err := Operation(operation).Scope()
			if err != nil || kind != ContentScope {
				return ErrSecurityUnavailable
			}
		}
		if !g.validAuthorityTarget() {
			return ErrSecurityUnavailable
		}
	}
	return nil
}

func (g accessGrant) validAuthorityTarget() bool {
	return g.targetScope == authorityTargetInstallation && g.typeID == "" && len(g.fields) == 0 ||
		g.targetScope == authorityTargetType && g.typeID != ""
}

func coversFields(source, requested []string) bool {
	for _, field := range requested {
		if !slices.Contains(source, field) {
			return false
		}
	}
	return true
}

func coversObject(source, requested string) bool {
	return source == requested || source == objectScopeAny && requested == "assigned"
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
