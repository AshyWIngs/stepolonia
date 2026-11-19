package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func (g RoleGrant) access() accessGrant {
	return accessGrant{operation: g.Operation, scope: g.Scope, typeID: g.TypeID, objectScope: g.ObjectScope, delegable: g.CanDelegate, targetScope: g.TargetScope, ceiling: g.ObjectCeiling, mayDelegate: g.MayDelegateContent, operations: g.DelegateOperations, fields: g.FieldIDs}
}

func accessGrants(grants []RoleGrant) []accessGrant {
	out := make([]accessGrant, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.access())
	}
	return out
}

func validateRoleInput(ctx context.Context, tx *sql.Tx, input RoleInput) ([]accessGrant, error) {
	if _, err := normalizeRoleName(input.Name); err != nil {
		return nil, err
	}
	if input.Grants == nil || len(input.Grants) > MaxRoleGrants {
		return nil, ErrValidation
	}
	seen := make(map[RoleGrantKey]bool, len(input.Grants))
	for _, grant := range input.Grants {
		if err := grant.validate(); err != nil {
			return nil, err
		}
		key := grant.key()
		if seen[key] {
			return nil, ErrValidation
		}
		seen[key] = true
		if err := validateGrantReferences(ctx, tx, grant); err != nil {
			return nil, err
		}
	}
	return accessGrants(input.Grants), nil
}

func (g RoleGrant) validate() error {
	kind, err := Operation(g.Operation).Scope()
	if err != nil || !uniqueStrings(g.FieldIDs) || !uniqueStrings(g.DelegateOperations) {
		return ErrValidation
	}
	for _, field := range g.FieldIDs {
		if !validGrantID(field) {
			return ErrValidation
		}
	}
	if kind == ContentScope {
		if g.Scope != grantScopeContent || !validGrantID(g.TypeID) || !g.validContentObject() || g.hasAuthorityMetadata() {
			return ErrValidation
		}
		return nil
	}
	return g.validateInstallation()
}

func (g RoleGrant) validateInstallation() error {
	if g.Scope != grantScopeInstallation || g.ObjectScope != "" {
		return ErrValidation
	}
	if g.Operation != opContentDelegate {
		if g.TypeID != "" || len(g.FieldIDs) != 0 || g.hasAuthorityMetadata() {
			return ErrValidation
		}
		return nil
	}
	return g.validateAuthority()
}

func (g RoleGrant) validateAuthority() error {
	if len(g.DelegateOperations) == 0 || len(g.DelegateOperations) > len(bootstrapContentOperations) || g.ObjectCeiling != objectScopeAssigned && g.ObjectCeiling != objectScopeAny {
		return ErrValidation
	}
	for _, operation := range g.DelegateOperations {
		kind, err := Operation(operation).Scope()
		if err != nil || kind != ContentScope {
			return ErrValidation
		}
	}
	if g.TargetScope == authorityTargetInstallation {
		if g.TypeID != "" || len(g.FieldIDs) != 0 {
			return ErrValidation
		}
	} else if g.TargetScope != authorityTargetType || !validGrantID(g.TypeID) {
		return ErrValidation
	}
	return nil
}

func (g RoleGrant) validContentObject() bool {
	if g.Operation == opContentCreate {
		return g.ObjectScope == objectScopeType
	}
	return g.ObjectScope == objectScopeAssigned || g.ObjectScope == objectScopeAny
}

func (g RoleGrant) hasAuthorityMetadata() bool {
	return g.TargetScope != "" || g.ObjectCeiling != "" || g.MayDelegateContent || len(g.DelegateOperations) != 0
}

func uniqueStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validateGrantReferences(ctx context.Context, tx *sql.Tx, g RoleGrant) error {
	if g.TypeID == "" {
		return nil
	}
	fields := g.FieldIDs
	if fields == nil {
		fields = []string{}
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.content_types WHERE id=$1) AND (SELECT count(*) FROM public.content_fields WHERE type_id=$1 AND field_id=ANY($2::uuid[]))=$3`, g.TypeID, fields, len(fields)).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrValidation
	}
	return nil
}

func queryRoleGrants(ctx context.Context, tx *sql.Tx, role string) ([]RoleGrant, error) {
	out := []RoleGrant{}
	rows, err := tx.QueryContext(ctx, `SELECT g.operation,g.scope,COALESCE(g.type_id::text,''),COALESCE(t.key,''),
 COALESCE(g.object_scope,''),g.can_delegate,COALESCE(g.target_scope,''),COALESCE(g.object_ceiling,''),
 g.may_delegate_content,array_to_json(g.delegate_operations)::text,
 COALESCE((SELECT json_agg(f.field_id::text ORDER BY f.field_id)::text FROM public.grant_fields f WHERE f.grant_id=g.id),'[]')
 FROM public.role_grants g LEFT JOIN public.content_types t ON t.id=g.type_id
 WHERE g.role_id=$1 ORDER BY g.operation,g.type_id,g.object_scope,g.id`, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var g RoleGrant
		var operations, fields string
		if err = rows.Scan(&g.Operation, &g.Scope, &g.TypeID, &g.collectionKey, &g.ObjectScope, &g.CanDelegate, &g.TargetScope, &g.ObjectCeiling, &g.MayDelegateContent, &operations, &fields); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(operations), &g.DelegateOperations); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(fields), &g.FieldIDs); err != nil {
			return nil, err
		}
		if err = g.validate(); err != nil {
			return nil, ErrSecurityUnavailable
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func replaceRoleGrants(ctx context.Context, tx *sql.Tx, role string, grants []RoleGrant) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.grant_fields WHERE grant_id IN (SELECT id FROM public.role_grants WHERE role_id=$1)`, role); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.role_grants WHERE role_id=$1`, role); err != nil {
		return err
	}
	for _, grant := range grants {
		if err := insertRoleGrant(ctx, tx, role, "", grant); err != nil {
			return err
		}
	}
	return nil
}

func insertRoleGrant(ctx context.Context, tx *sql.Tx, role, id string, grant RoleGrant) error {
	if id == "" {
		var err error
		id, err = randomID()
		if err != nil {
			return err
		}
	}
	operations := grant.DelegateOperations
	if operations == nil {
		operations = []string{}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope,can_delegate,delegate_operations,target_scope,object_ceiling,may_delegate_content) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,''),$7,$8,NULLIF($9,''),NULLIF($10,''),$11)`, id, role, grant.Operation, grant.Scope, grant.TypeID, grant.ObjectScope, grant.CanDelegate, operations, grant.TargetScope, grant.ObjectCeiling, grant.MayDelegateContent)
	if err != nil || len(grant.FieldIDs) == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.grant_fields(grant_id,type_id,field_id) SELECT $1,$2,field_id FROM unnest($3::uuid[]) AS field_id`, id, grant.TypeID, grant.FieldIDs)
	return err
}
func roleRecipients(ctx context.Context, tx *sql.Tx, role string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id::text FROM public.user_roles WHERE role_id=$1 ORDER BY user_id`, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func recipientsWithinEnvelope(actor []accessGrant, snapshot accessSnapshot, recipients []string) bool {
	for _, user := range recipients {
		if !withinEnvelope(actor, snapshot[user].latent) {
			return false
		}
	}
	return true
}

// Each field retains the same complete operation/object/delegation coverage.
// Compatible sources may cover separate fields; an assigned source never fills
// a missing field in an any-scope grant.
func accessDoesNotGrow(before, after []accessGrant) bool {
	for _, target := range after {
		// A complete covering grant also covers every individual field. Keep
		// compatible-source union as the fallback for genuinely split policies.
		if hasAccessCoverage(before, target) {
			continue
		}
		if len(target.fields) == 0 {
			return false
		}
		for _, field := range target.fields {
			part := target
			part.fields = []string{field}
			if !hasAccessCoverage(before, part) {
				return false
			}
		}
	}
	return true
}

func hasAccessCoverage(before []accessGrant, target accessGrant) bool {
	for _, source := range before {
		if source.preservesAccess(target) {
			return true
		}
	}
	return false
}

func (source accessGrant) preservesAccess(target accessGrant) bool {
	if source.operation != target.operation || target.delegable && !source.delegable {
		return false
	}
	if target.scope == grantScopeContent {
		return source.typeID == target.typeID && coversObject(source.objectScope, target.objectScope) && coversFields(source.fields, target.fields)
	}
	if target.operation != opContentDelegate {
		return true
	}
	return coversFields(source.operations, target.operations) && (!target.mayDelegate || source.mayDelegate) && coversObject(source.ceiling, target.ceiling) && source.coversAuthorityTarget(target)
}

func (source accessGrant) coversAuthorityTarget(target accessGrant) bool {
	return source.targetScope == authorityTargetInstallation || source.targetScope == target.targetScope && source.typeID == target.typeID && coversFields(source.fields, target.fields)
}

func normalizeRoleInput(input RoleInput) RoleInput {
	out := RoleInput{Name: input.Name}
	if input.Grants == nil {
		return out
	}
	out.Grants = make([]RoleGrant, len(input.Grants))
	for i, g := range input.Grants {
		g.TypeID = strings.ToLower(g.TypeID)
		g.FieldIDs = append([]string{}, g.FieldIDs...)
		for j := range g.FieldIDs {
			g.FieldIDs[j] = strings.ToLower(g.FieldIDs[j])
		}
		g.DelegateOperations = append([]string{}, g.DelegateOperations...)
		out.Grants[i] = g
	}
	return out
}
