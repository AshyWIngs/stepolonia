package identity

import (
	"context"
	"database/sql"
	"errors"
)

func hasAdminAuthority(grants []accessGrant) bool {
	required := []string{}
	for _, operation := range bootstrapInstallationOperations {
		switch operation {
		case string(OpContentTypesManage), opContentInitializeAccess, opContentDelegate, opSettingsManage, opSessionPolicyManage:
			continue
		default:
			required = append(required, operation)
		}
	}
	for _, operation := range required {
		if !hasDelegableOperation(grants, operation) {
			return false
		}
	}
	for _, g := range grants {
		if fullContentAuthority(g) {
			return true
		}
	}
	return false
}

func hasDelegableOperation(grants []accessGrant, operation string) bool {
	for _, grant := range grants {
		if grant.operation == operation && grant.delegable {
			return true
		}
	}
	return false
}

func fullContentAuthority(g accessGrant) bool {
	return g.operation == opContentDelegate && g.delegable && g.targetScope == authorityTargetInstallation &&
		g.ceiling == objectScopeAny && g.mayDelegate && coversFields(g.operations, bootstrapContentOperations)
}

func (s *AuthService) requireAccessAdmin(ctx context.Context, tx *sql.Tx, access accessSnapshot) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM public.users WHERE state='active' ORDER BY id`)
	if err != nil {
		return err
	}
	var candidates []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if hasAdminAuthority(access[id].active) {
			candidates = append(candidates, id)
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range candidates {
		err = s.requireEnrolledFactors(ctx, tx, id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrInvalidTransition) {
			return err
		}
	}
	return ErrLastAdminRequired
}
