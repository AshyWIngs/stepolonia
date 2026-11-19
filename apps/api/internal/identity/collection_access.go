package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	collectionCreatedEvent  = "content.collection_created"
	collectionRevisedEvent  = "content.collection_revised"
	collectionEnabledEvent  = "content.collection_enabled"
	collectionDisabledEvent = "content.collection_disabled"
	initialAccessEvent      = "content.initial_access_granted"
	newFieldAccessEvent     = "content.new_field_access_granted"
)

// CollectionMutation describes fields newly created by the Registry's write
// callback. Identity verifies the resulting definitions before granting access.
type CollectionMutation struct {
	TypeID         string
	SchemaNo       int64
	PreviousSchema int64
	NewFieldIDs    []string
}

// CollectionAccess records the exact policy added by this committed command.
type CollectionAccess struct {
	RoleID string
	Grants []RoleGrant
}

type collectionWriter func(context.Context, *sql.Tx) (CollectionMutation, error)

// WithCollectionRead authorizes Registry maintenance reads under the same
// mutex as policy revocation.
func (s *AuthService) WithCollectionRead(ctx context.Context, req AuthRequest, read func(context.Context, *sql.Tx) error) error {
	return s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requirePermission(ctx, tx, session.user, string(OpContentTypesManage)); err != nil {
			return err
		}
		return read(ctx, tx)
	})
}

func (s *AuthService) WithCollectionCreate(ctx context.Context, req AuthRequest, initialize bool, create collectionWriter) (CollectionAccess, error) {
	return s.withCollectionMutation(ctx, req, collectionCreatedEvent, initialize, create)
}

func (s *AuthService) WithCollectionRevision(ctx context.Context, req AuthRequest, initialize bool, revise collectionWriter) (CollectionAccess, error) {
	return s.withCollectionMutation(ctx, req, collectionRevisedEvent, initialize, revise)
}

// WithCollectionAvailability commits the availability change and its audit
// entry under the same lock as authoring and permission changes.
func (s *AuthService) WithCollectionAvailability(ctx context.Context, req AuthRequest, enabled bool, write func(context.Context, *sql.Tx) (string, error)) error {
	if write == nil {
		return ErrSecurityUnavailable
	}
	event := collectionDisabledEvent
	if enabled {
		event = collectionEnabledEvent
	}
	return s.authorizedCommand(ctx, req, []string{string(OpContentTypesManage)}, event, func(ctx context.Context, tx *sql.Tx, session Session) error {
		typeID, err := write(ctx, tx)
		if err != nil {
			return err
		}
		return securityAudit(ctx, tx, event, session.user, typeID, "")
	})
}

func (s *AuthService) withCollectionMutation(ctx context.Context, req AuthRequest, event string, initialize bool, write collectionWriter) (CollectionAccess, error) {
	permissions := collectionWritePermissions(event, initialize)
	var access CollectionAccess
	err := s.authorizedCommandWithFreshness(ctx, req, permissions, event, initialize, func(ctx context.Context, tx *sql.Tx, session Session) error {
		var before accessSnapshot
		if initialize {
			var err error
			before, err = loadAccess(ctx, tx)
			if err != nil {
				return err
			}
		}
		change, err := write(ctx, tx)
		if err != nil {
			return err
		}
		if initialize {
			access, err = s.initializeCollectionAccess(ctx, tx, session.user, before, change, event == collectionCreatedEvent)
			if err != nil {
				return err
			}
		}
		return securityAudit(ctx, tx, event, session.user, change.TypeID, "")
	})
	if err != nil {
		return CollectionAccess{}, err
	}
	return access, nil
}

func collectionWritePermissions(event string, initialize bool) []string {
	permissions := []string{string(OpContentTypesManage)}
	if initialize {
		permissions = append(permissions, opContentInitializeAccess)
		if event == collectionRevisedEvent {
			permissions = append(permissions, opRolesEdit)
		}
	}
	return permissions
}

func (s *AuthService) initializeCollectionAccess(ctx context.Context, tx *sql.Tx, actor string, before accessSnapshot, change CollectionMutation, fresh bool) (CollectionAccess, error) {
	if err := checkNewCollectionFields(ctx, tx, actor, change, fresh); err != nil {
		return CollectionAccess{}, err
	}
	grants := newCollectionGrants(before[actor].active, change, fresh)
	if len(grants) == 0 || !withinEnvelope(before[actor].active, accessGrants(grants)) {
		return CollectionAccess{}, ErrPermissionDenied
	}
	roleID, err := findOrCreateBoundRole(ctx, tx, actor, change.TypeID, fresh)
	if err != nil {
		return CollectionAccess{}, err
	}
	for _, grant := range grants {
		if err := addBoundRoleGrant(ctx, tx, roleID, grant); err != nil {
			return CollectionAccess{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.roles SET state='active',version=version+1 WHERE id=$1`, roleID); err != nil {
		return CollectionAccess{}, err
	}
	after, err := loadAccess(ctx, tx)
	if err != nil {
		return CollectionAccess{}, err
	}
	if err := checkCollectionAccessDelta(before, after, actor, grants); err != nil {
		return CollectionAccess{}, err
	}
	if err := s.requireAccessAdmin(ctx, tx, after); err != nil {
		return CollectionAccess{}, err
	}
	event := newFieldAccessEvent
	if fresh {
		event = initialAccessEvent
	}
	if err := recordCollectionAccess(ctx, tx, event, actor, roleID, change, grants); err != nil {
		return CollectionAccess{}, err
	}
	return CollectionAccess{RoleID: roleID, Grants: grants}, nil
}

func checkNewCollectionFields(ctx context.Context, tx *sql.Tx, actor string, change CollectionMutation, fresh bool) error {
	if len(change.NewFieldIDs) == 0 || !uniqueStrings(change.NewFieldIDs) {
		return ErrPermissionDenied
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM public.content_types WHERE id=$1 AND current_schema_no=$2 AND (NOT $4::boolean OR version=1))
 AND ($2::bigint=$3::bigint+1)
 AND (NOT $4::boolean OR ($3=0 AND NOT EXISTS (SELECT 1 FROM public.content_items WHERE type_id=$1)))
 AND (SELECT count(*) FROM public.content_field_definitions d
      WHERE d.type_id=$1 AND d.schema_no=$2 AND d.field_id=ANY($5::uuid[])
      AND ($4 OR (NOT d.required AND NOT EXISTS (
          SELECT 1 FROM public.content_field_definitions old
          WHERE old.type_id=d.type_id AND old.field_id=d.field_id AND old.schema_no<d.schema_no
      ))))=cardinality($5::uuid[])
 AND (NOT $4 OR (SELECT count(*) FROM public.content_field_definitions WHERE type_id=$1 AND schema_no=$2)=cardinality($5::uuid[]))
 AND NOT EXISTS (
 SELECT 1 FROM public.content_field_definitions d
 WHERE d.type_id=$1 AND d.schema_no=$2 AND d.field_id=ANY($5::uuid[]) AND d.target_type_id IS NOT NULL
 AND NOT EXISTS (
 SELECT 1 FROM public.role_grants g
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$6
 WHERE g.type_id=d.target_type_id AND g.operation=$7 AND g.scope='content' AND g.object_scope IN ('any','assigned')
 ))`, change.TypeID, change.SchemaNo, change.PreviousSchema, fresh, change.NewFieldIDs, actor, OpContentRead).Scan(&valid)
	if err == nil && !valid {
		return ErrPermissionDenied
	}
	return err
}

func newCollectionGrants(active []accessGrant, change CollectionMutation, fresh bool) []RoleGrant {
	fieldOperations := []string{string(OpContentRead), string(OpContentUpdate), string(OpContentReady), string(OpContentPublish)}
	grants := []RoleGrant{}
	if fresh {
		grants = append(grants, RoleGrant{Operation: opContentCreate, Scope: grantScopeContent, TypeID: change.TypeID, ObjectScope: objectScopeType})
		fieldOperations = append(fieldOperations, string(OpContentUnpublish))
	}
	for _, operation := range fieldOperations {
		grant := RoleGrant{Operation: operation, Scope: grantScopeContent, TypeID: change.TypeID,
			ObjectScope: objectScopeAny, FieldIDs: change.NewFieldIDs}
		if !fresh {
			grant.ObjectScope = newFieldAccessScope(active, grant)
		}
		if grant.ObjectScope != "" {
			grants = append(grants, grant)
		}
	}
	return grants
}

func newFieldAccessScope(active []accessGrant, grant RoleGrant) string {
	for _, scope := range []string{objectScopeAny, objectScopeAssigned} {
		grant.ObjectScope = scope
		for _, access := range active {
			if access.operation == grant.Operation && access.typeID == grant.TypeID && len(access.fields) != 0 &&
				coversObject(access.objectScope, scope) && withinEnvelope(active, []accessGrant{grant.access()}) {
				return scope
			}
		}
	}
	return ""
}

func findOrCreateBoundRole(ctx context.Context, tx *sql.Tx, actor, typeID string, fresh bool) (string, error) {
	var id, state string
	var member bool
	err := tx.QueryRowContext(ctx, `SELECT r.id::text,r.state,EXISTS (
 SELECT 1 FROM public.user_roles m WHERE m.role_id=r.id AND m.user_id=$1)
 FROM public.roles r WHERE r.self_access_user_id=$1 AND r.self_access_type_id=$2`, actor, typeID).Scan(&id, &state, &member)
	if err == nil {
		if fresh || state != roleStateActive || !member {
			return "", ErrPermissionDenied
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return createBoundRole(ctx, tx, actor, typeID)
}

func createBoundRole(ctx context.Context, tx *sql.Tx, actor, typeID string) (string, error) {
	for _, permission := range []string{opRolesCreate, opRolesAssign, opRolesEnable} {
		if err := requirePermission(ctx, tx, actor, permission); err != nil {
			return "", err
		}
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM public.roles)<$2
 AND (SELECT count(*) FROM public.user_roles WHERE user_id=$1)<$3`, actor, maxRoles, MaxUserRoles).Scan(&allowed)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrInvalidTransition
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	roleName := "Collection access " + id
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.roles(id,name,self_access_user_id,self_access_type_id)
 VALUES($1,$2,$3,$4)`, id, roleName, actor, typeID); err != nil {
		return "", roleWriteError(err)
	}
	_, err = tx.ExecContext(ctx, `WITH assigned AS (
 INSERT INTO public.user_roles(user_id,role_id) VALUES($1,$2) RETURNING user_id
 ) UPDATE public.users SET version=version+1 WHERE id IN (SELECT user_id FROM assigned)`, actor, id)
	return id, err
}

func addBoundRoleGrant(ctx context.Context, tx *sql.Tx, roleID string, grant RoleGrant) error {
	var grantID string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM public.role_grants
 WHERE role_id=$1 AND operation=$2 AND type_id=$3 AND object_scope=$4`,
		roleID, grant.Operation, grant.TypeID, grant.ObjectScope).Scan(&grantID)
	if errors.Is(err, sql.ErrNoRows) {
		grantID, err = randomID()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES($1,$2,$3,'content',$4,$5)`, grantID, roleID, grant.Operation, grant.TypeID, grant.ObjectScope)
	}
	if err != nil || len(grant.FieldIDs) == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT $1,$2,id FROM unnest($3::uuid[]) AS fields(id) ON CONFLICT DO NOTHING`,
		grantID, grant.TypeID, grant.FieldIDs)
	return err
}

func checkCollectionAccessDelta(before, after accessSnapshot, actor string, grants []RoleGrant) error {
	for user, current := range after {
		if user != actor && !ownAccessDoesNotGrow(before[user], current) {
			return ErrPermissionDenied
		}
	}
	allowed := before[actor]
	added := accessGrants(grants)
	allowed.active = append(append([]accessGrant{}, allowed.active...), added...)
	allowed.latent = append(append([]accessGrant{}, allowed.latent...), added...)
	if !ownAccessDoesNotGrow(allowed, after[actor]) ||
		!accessDoesNotGrow(after[actor].active, added) {
		return ErrPermissionDenied
	}
	return nil
}

func recordCollectionAccess(ctx context.Context, tx *sql.Tx, event, actor, roleID string, change CollectionMutation, grants []RoleGrant) error {
	type grantDetail struct {
		Operation string   `json:"operation"`
		Scope     string   `json:"object_scope"`
		FieldIDs  []string `json:"field_ids,omitempty"`
	}
	reason := "explicit_initial_access"
	if event == newFieldAccessEvent {
		reason = "explicit_new_field_access"
	}
	details := struct {
		Reason string        `json:"reason"`
		RoleID string        `json:"role_id"`
		Schema int64         `json:"schema_no"`
		Grants []grantDetail `json:"grants"`
	}{Reason: reason, RoleID: roleID, Schema: change.SchemaNo}
	for _, grant := range grants {
		details.Grants = append(details.Grants, grantDetail{grant.Operation, grant.ObjectScope, grant.FieldIDs})
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.security_audit(id,operation,actor_id,target_id,request_id,details)
 VALUES($1,$2,$3,$4,$5,$6)`, id, event, actor, change.TypeID, ctx.Value(auditRequestKey{}), raw)
	return err
}
