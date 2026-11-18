package identity

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

// MaxRoleGrantOptions bounds the number of distinct grant shapes returned together.
const MaxRoleGrantOptions = 512

const (
	maxGrantOptionTypes         = 64
	maxGrantOptionFieldsPerType = 128
)

// RoleGrantOption is one eligible grant shape. Its fields and delegated
// operations can be narrowed by the role editor.
type RoleGrantOption struct {
	RoleGrant
	TypeKey string
	Fields  []RoleGrantField
}

// RoleGrantField identifies authorized registry metadata without any field values.
type RoleGrantField struct {
	ID, Key string
}

type eligibleGrantType struct {
	ID, Key string
}

// ReadRoleGrantOptions describes current delegation authority for role editors.
// Choices never combine separate sources to cover one requested grant.
func (s *AuthService) ReadRoleGrantOptions(ctx context.Context, req AuthRequest) ([]RoleGrantOption, error) {
	var out []RoleGrantOption
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requireRolePolicyManager(ctx, tx, session.user); err != nil {
			return err
		}
		access, err := loadAccess(ctx, tx)
		if err != nil {
			return err
		}
		out, err = collectRoleGrantOptions(ctx, tx, access[session.user].active)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func requireRolePolicyManager(ctx context.Context, tx *sql.Tx, userID string) error {
	err := requirePermission(ctx, tx, userID, opRolesCreate)
	if errors.Is(err, ErrPermissionDenied) {
		return requirePermission(ctx, tx, userID, opRolesEdit)
	}
	return err
}

func collectRoleGrantOptions(ctx context.Context, tx *sql.Tx, sources []accessGrant) ([]RoleGrantOption, error) {
	index := make(map[string]RoleGrantOption)
	typeIDs, anyType := installationGrantOptions(sources, index)
	types, err := grantOptionTypes(ctx, tx, typeIDs, anyType)
	if err != nil {
		return nil, err
	}
	for _, resource := range types {
		if err := addTypeGrantOptions(ctx, tx, resource, sources, index); err != nil {
			return nil, err
		}
		if len(index) > MaxRoleGrantOptions {
			return nil, ErrSecurityUnavailable
		}
	}
	keys := make([]string, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]RoleGrantOption, 0, len(keys))
	for _, key := range keys {
		out = append(out, index[key])
	}
	return out, nil
}

func installationGrantOptions(sources []accessGrant, index map[string]RoleGrantOption) ([]string, bool) {
	var anyType bool
	knownTypes := make(map[string]bool)
	for _, source := range sources {
		switch {
		case source.operation == opContentDelegate:
			if source.targetScope == authorityTargetType {
				knownTypes[source.typeID] = true
				continue
			}
			anyType = true
			if source.delegable {
				addGrantOption(index, newAuthorityOption(source, eligibleGrantType{}, nil))
			}
		case source.scope == grantScopeContent && source.delegable:
			knownTypes[source.typeID] = true
		case source.scope == grantScopeInstallation && source.delegable:
			addGrantOption(index, RoleGrantOption{RoleGrant: RoleGrant{Operation: source.operation, Scope: grantScopeInstallation, CanDelegate: true}})
		}
	}
	typeIDs := make([]string, 0, len(knownTypes))
	for id := range knownTypes {
		typeIDs = append(typeIDs, id)
	}
	return typeIDs, anyType
}

func grantOptionTypes(ctx context.Context, tx *sql.Tx, typeIDs []string, anyType bool) ([]eligibleGrantType, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,key FROM public.content_types
 WHERE $1 OR id=ANY($2::uuid[]) ORDER BY key,id LIMIT $3`, anyType, typeIDs, maxGrantOptionTypes+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]eligibleGrantType, 0)
	for rows.Next() {
		var resource eligibleGrantType
		if err := rows.Scan(&resource.ID, &resource.Key); err != nil {
			return nil, err
		}
		out = append(out, resource)
	}
	if len(out) > maxGrantOptionTypes {
		return nil, ErrSecurityUnavailable
	}
	return out, rows.Err()
}

func addTypeGrantOptions(ctx context.Context, tx *sql.Tx, resource eligibleGrantType, sources []accessGrant, index map[string]RoleGrantOption) error {
	broad, narrow, allowedIDs := grantSourcesForType(resource.ID, sources)
	allFields := slices.ContainsFunc(broad, grantSourceUsesFields)
	fields, err := grantOptionFields(ctx, tx, resource.ID, allFields, allowedIDs)
	if err != nil {
		return err
	}
	for _, source := range broad {
		addGrantSource(index, source, resource, fields)
	}
	for _, source := range narrow {
		visible := slices.DeleteFunc(slices.Clone(fields), func(field RoleGrantField) bool {
			return !slices.Contains(source.fields, field.ID)
		})
		addGrantSource(index, source, resource, visible)
	}
	return nil
}

func grantSourcesForType(typeID string, sources []accessGrant) (broad, narrow []accessGrant, fields []string) {
	fields = []string{}
	for _, source := range sources {
		switch {
		case source.operation == opContentDelegate && source.targetScope == authorityTargetInstallation:
			broad = append(broad, source)
			continue
		case source.typeID != typeID || source.operation != opContentDelegate && (source.scope != grantScopeContent || !source.delegable):
			continue
		}
		narrow = append(narrow, source)
		if grantSourceUsesFields(source) {
			fields = append(fields, source.fields...)
		}
	}
	return broad, narrow, fields
}

func grantSourceUsesFields(source accessGrant) bool {
	if source.operation == opContentDelegate {
		return slices.ContainsFunc(source.operations, grantOptionUsesFields)
	}
	return grantOptionUsesFields(source.operation)
}

func grantOptionUsesFields(operation string) bool {
	switch Operation(operation) {
	case OpContentRead, OpContentUpdate, OpContentReady, OpContentPublish:
		return true
	default:
		return false
	}
}

func grantOptionFields(ctx context.Context, tx *sql.Tx, typeID string, all bool, fieldIDs []string) ([]RoleGrantField, error) {
	if !all && len(fieldIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT field_id::text,key FROM public.content_fields
 WHERE type_id=$1 AND ($2 OR field_id=ANY($3::uuid[]))
 ORDER BY key,field_id LIMIT $4`, typeID, all, fieldIDs, maxGrantOptionFieldsPerType+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]RoleGrantField, 0)
	for rows.Next() {
		var field RoleGrantField
		if err := rows.Scan(&field.ID, &field.Key); err != nil {
			return nil, err
		}
		out = append(out, field)
	}
	if len(out) > maxGrantOptionFieldsPerType {
		return nil, ErrSecurityUnavailable
	}
	return out, rows.Err()
}

func addGrantSource(index map[string]RoleGrantOption, source accessGrant, resource eligibleGrantType, fields []RoleGrantField) {
	if source.operation == opContentDelegate {
		if source.delegable {
			addGrantOption(index, newAuthorityOption(source, resource, fields))
		}
		for _, operation := range source.operations {
			addContentGrantOptions(index, resource, operation, source.ceiling, source.mayDelegate, fields)
		}
		return
	}
	addContentGrantOptions(index, resource, source.operation, source.objectScope, true, fields)
}

func newAuthorityOption(source accessGrant, resource eligibleGrantType, fields []RoleGrantField) RoleGrantOption {
	if !grantSourceUsesFields(source) {
		fields = nil
	}
	target := authorityTargetInstallation
	if resource.ID != "" {
		target = authorityTargetType
	}
	return RoleGrantOption{
		RoleGrant: RoleGrant{
			Operation: opContentDelegate, Scope: grantScopeInstallation, CanDelegate: true, TypeID: resource.ID,
			TargetScope: target, ObjectCeiling: source.ceiling, MayDelegateContent: source.mayDelegate,
			DelegateOperations: slices.Clone(source.operations),
		},
		TypeKey: resource.Key,
		Fields:  fields,
	}
}

func addContentGrantOptions(index map[string]RoleGrantOption, resource eligibleGrantType, operation, ceiling string, delegable bool, fields []RoleGrantField) {
	if !grantOptionUsesFields(operation) {
		fields = nil
	}
	scopes := []string{objectScopeAssigned}
	if operation == opContentCreate {
		scopes = []string{objectScopeType}
	} else if ceiling == objectScopeAny {
		scopes = append(scopes, objectScopeAny)
	}
	for _, scope := range scopes {
		addGrantOption(index, RoleGrantOption{
			RoleGrant: RoleGrant{Operation: operation, Scope: grantScopeContent, TypeID: resource.ID, ObjectScope: scope, CanDelegate: delegable},
			TypeKey:   resource.Key, Fields: fields,
		})
	}
}

func addGrantOption(index map[string]RoleGrantOption, option RoleGrantOption) {
	slices.Sort(option.DelegateOperations)
	ids := make([]string, 0, len(option.Fields))
	for _, field := range option.Fields {
		ids = append(ids, field.ID)
	}
	key := strings.Join([]string{
		option.Operation, option.TypeKey, option.ObjectScope, option.ObjectCeiling,
		strings.Join(option.DelegateOperations, ","), strings.Join(ids, ","),
	}, "/")
	if option.MayDelegateContent {
		key += "/delegate"
	}
	if current, exists := index[key]; !exists || !current.CanDelegate && option.CanDelegate {
		index[key] = option
	}
}
