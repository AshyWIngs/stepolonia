package cms

import (
	"context"
	"database/sql"
	"errors"
)

const (
	draftCreatedEvent   = "content.draft_created"
	draftSavedEvent     = "content.draft_saved"
	draftReadyEvent     = "content.draft_ready"
	draftPublishedEvent = "content.draft_published"
)

// WithDraftCreate authorizes a new draft on the requested collection and commits
// the creator assignment and success audit with the draft.
func (s *AuthService) WithDraftCreate(ctx context.Context, req AuthRequest, typeKey string, expectedSchemaNo int64, create func(ctx context.Context, tx *sql.Tx, typeID, itemID string) error) error {
	if create == nil {
		return ErrSecurityUnavailable
	}
	return s.auditedCommand(ctx, req, draftCreatedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if !session.FreshStepUp() {
			return ErrStepUpRequired
		}
		typeID, schemaNo, err := draftCreateTarget(ctx, tx, session.user, typeKey)
		if err != nil {
			return err
		}
		if schemaNo != expectedSchemaNo {
			return ErrDraftSchemaConflict
		}
		itemID, err := randomID()
		if err != nil {
			return err
		}
		if err := create(ctx, tx, typeID, itemID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_item_assignments(item_id,user_id) VALUES($1,$2)`, itemID, session.user); err != nil {
			return err
		}
		return securityAudit(ctx, tx, draftCreatedEvent, session.user, itemID, "")
	})
}

func draftCreateTarget(ctx context.Context, tx *sql.Tx, userID, typeKey string) (string, int64, error) {
	var typeID string
	var schemaNo int64
	err := tx.QueryRowContext(ctx, `SELECT t.id::text,t.current_schema_no
 FROM public.content_types t WHERE t.key=$1 AND EXISTS (
 SELECT 1 FROM public.role_grants g
 JOIN public.permission_operations o ON o.key=g.operation AND o.scope='content'
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$2
 WHERE g.type_id=t.id AND g.operation=$3 AND g.scope='content' AND g.object_scope='type')`,
		typeKey, userID, opContentCreate).Scan(&typeID, &schemaNo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return typeID, schemaNo, err
}

// WithDraftUpdate checks the full session, CSRF and step-up under the installation
// mutex, and audits a successful Engine update in the same transaction.
func (s *AuthService) WithDraftUpdate(ctx context.Context, req AuthRequest, itemID string, update func(context.Context, *sql.Tx, Session, string) error) error {
	return s.withDraftCommand(ctx, req, draftSavedEvent, itemID, update)
}

// WithDraftCandidate executes an authenticated ready or publish command and
// commits its audit entry in the same transaction.
func (s *AuthService) WithDraftCandidate(ctx context.Context, req AuthRequest, operation Operation, itemID string, command func(context.Context, *sql.Tx, Session, string) error) error {
	switch operation {
	case OpContentReady:
		return s.withDraftCommand(ctx, req, draftReadyEvent, itemID, command)
	case OpContentPublish:
		return s.withDraftCommand(ctx, req, draftPublishedEvent, itemID, command)
	default:
		return ErrPermissionDenied
	}
}

func (s *AuthService) withDraftCommand(ctx context.Context, req AuthRequest, event, itemID string, command func(context.Context, *sql.Tx, Session, string) error) error {
	if command == nil {
		return ErrSecurityUnavailable
	}
	return s.auditedCommand(ctx, req, event, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if !session.FreshStepUp() {
			return ErrStepUpRequired
		}
		id, valid := canonicalMembershipID(itemID)
		if !valid {
			return ErrNotFound
		}
		if err := command(ctx, tx, session, id); err != nil {
			return err
		}
		return securityAudit(ctx, tx, event, session.user, id, "")
	})
}

// ContentItemAccess holds the exact field IDs granted for one operation on one item.
// Its contents are valid only inside the transaction that loaded them.
type ContentItemAccess struct {
	TypeID  string
	TypeKey string
	fields  map[string]struct{}
}

func (a ContentItemAccess) AllowsField(fieldID string) bool {
	_, ok := a.fields[fieldID]
	return ok
}

// LoadContentItemAccess resolves permissions against the persisted item type and
// assignment before collecting field IDs from the qualifying grants.
func (s Session) LoadContentItemAccess(ctx context.Context, tx *sql.Tx, operation Operation, itemID string) (ContentItemAccess, error) {
	if s.kind != sessionFull {
		return ContentItemAccess{}, ErrPermissionDenied
	}
	switch operation {
	case OpContentRead, OpContentUpdate, OpContentReady, OpContentPublish:
	default:
		return ContentItemAccess{}, ErrPermissionDenied
	}
	id, valid := canonicalMembershipID(itemID)
	if !valid {
		return ContentItemAccess{}, ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT i.type_id::text, t.key, f.field_id::text
 FROM public.content_items i
 JOIN public.content_types t ON t.id=i.type_id
 JOIN public.role_grants g ON g.type_id=i.type_id
 JOIN public.permission_operations o ON o.key=g.operation
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$1
 LEFT JOIN public.grant_fields f ON f.grant_id=g.id AND f.type_id=i.type_id
 WHERE i.id=$2 AND g.operation=$3 AND g.scope='content' AND o.scope='content'
 AND (g.object_scope='any' OR g.object_scope='assigned' AND EXISTS (
 SELECT 1 FROM public.content_item_assignments a WHERE a.item_id=i.id AND a.user_id=m.user_id))`,
		s.user, id, operation)
	if err != nil {
		return ContentItemAccess{}, err
	}
	defer func() { _ = rows.Close() }()
	access := ContentItemAccess{fields: make(map[string]struct{})}
	for rows.Next() {
		var field sql.NullString
		if err := rows.Scan(&access.TypeID, &access.TypeKey, &field); err != nil {
			return ContentItemAccess{}, err
		}
		if field.Valid {
			access.fields[field.String] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return ContentItemAccess{}, err
	}
	if access.TypeID == "" {
		return ContentItemAccess{}, ErrNotFound
	}
	return access, nil
}
