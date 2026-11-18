package cms

import (
	"context"
	"database/sql"
)

const draftSavedEvent = "content.draft_saved"

// WithDraftUpdate checks the full session, CSRF and step-up under the installation
// mutex, and audits a successful Engine update in the same transaction.
func (s *AuthService) WithDraftUpdate(ctx context.Context, req AuthRequest, itemID string, update func(context.Context, *sql.Tx, Session, string) error) error {
	if update == nil {
		return ErrSecurityUnavailable
	}
	return s.auditedCommand(ctx, req, draftSavedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if !session.FreshStepUp() {
			return ErrStepUpRequired
		}
		id, valid := canonicalMembershipID(itemID)
		if !valid {
			return ErrNotFound
		}
		if err := update(ctx, tx, session, id); err != nil {
			return err
		}
		return securityAudit(ctx, tx, draftSavedEvent, session.user, id, "")
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
	if s.kind != sessionFull || (operation != OpContentRead && operation != OpContentUpdate) {
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
