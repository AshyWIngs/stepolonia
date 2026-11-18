package identity

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
)

const contentAssigneesReplacedEvent = "content.assignees_replaced"

// MaxContentAssignees bounds the complete assignee set of one item.
const MaxContentAssignees = maxUsers

var contentTypeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ContentAssignees is the resulting assignee set and optimistic item version.
type ContentAssignees struct {
	ItemID      string
	Version     int64
	AssigneeIDs []string
}

// ReplaceContentAssignees replaces every assignment on an existing item.
func (s *AuthService) ReplaceContentAssignees(ctx context.Context, req AuthRequest, typeKey, itemID string, expectedVersion int64, assigneeIDs []string) (ContentAssignees, error) {
	var out ContentAssignees
	err := s.auditedCommand(ctx, req, contentAssigneesReplacedEvent, func(ctx context.Context, tx *sql.Tx, x Session) error {
		var err error
		out, err = replaceContentAssignees(ctx, tx, x, contentAssigneeReplacement{typeKey, itemID, expectedVersion, assigneeIDs})
		return err
	})
	if err != nil {
		return ContentAssignees{}, err
	}
	return out, nil
}

type contentAssigneeReplacement struct {
	typeKey, itemID string
	version         int64
	assigneeIDs     []string
}

func replaceContentAssignees(ctx context.Context, tx *sql.Tx, x Session, command contentAssigneeReplacement) (ContentAssignees, error) {
	item, err := assignmentItem(ctx, tx, command, x)
	if err != nil {
		return ContentAssignees{}, err
	}
	requested, err := canonicalAssigneeIDs(command.assigneeIDs)
	if err != nil {
		return ContentAssignees{}, err
	}
	access, err := loadAccess(ctx, tx)
	if err != nil {
		return ContentAssignees{}, err
	}
	if err = validateAssigneeRecipients(ctx, tx, item, x.user, requested, access); err != nil {
		return ContentAssignees{}, err
	}
	if item.version != command.version {
		return ContentAssignees{}, ErrVersionConflict
	}
	version, err := writeContentAssignments(ctx, tx, item.id, requested)
	if err != nil {
		return ContentAssignees{}, err
	}
	return ContentAssignees{ItemID: item.id, Version: version, AssigneeIDs: requested},
		securityAudit(ctx, tx, contentAssigneesReplacedEvent, x.user, item.id, "")
}

type assigneeItem struct {
	id, typeID string
	version    int64
}

func assignmentItem(ctx context.Context, tx *sql.Tx, command contentAssigneeReplacement, actor Session) (assigneeItem, error) {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM public.user_roles m
 JOIN public.roles r ON r.id=m.role_id
 JOIN public.role_grants g ON g.role_id=m.role_id
 JOIN public.permission_operations o ON o.key=g.operation
 WHERE m.user_id=$1 AND r.state='active' AND g.operation=$2
 AND g.scope='content' AND o.scope='content' AND g.object_scope='any')`, actor.user, opContentAssign).Scan(&allowed)
	if err != nil {
		return assigneeItem{}, err
	}
	if !allowed {
		return assigneeItem{}, ErrPermissionDenied
	}
	if !actor.FreshStepUp() {
		return assigneeItem{}, ErrStepUpRequired
	}
	id, valid := canonicalMembershipID(command.itemID)
	if !valid || !contentTypeKeyPattern.MatchString(command.typeKey) || command.version < 1 {
		return assigneeItem{}, ErrValidation
	}
	item := assigneeItem{id: id}
	err = tx.QueryRowContext(ctx, `SELECT i.type_id::text, i.version FROM public.content_items i
 JOIN public.content_types t ON t.id=i.type_id
 WHERE i.id=$3 AND t.key=$4 AND i.type_id IN (
 SELECT g.type_id FROM public.user_roles m
 JOIN public.roles r ON r.id=m.role_id
 JOIN public.role_grants g ON g.role_id=m.role_id
 JOIN public.permission_operations o ON o.key=g.operation
 WHERE m.user_id=$1 AND r.state='active' AND g.operation=$2
 AND g.scope='content' AND o.scope='content' AND g.object_scope='any')`,
		actor.user, opContentAssign, item.id, command.typeKey).Scan(&item.typeID, &item.version)
	if errors.Is(err, sql.ErrNoRows) {
		return assigneeItem{}, ErrNotFound
	}
	if err != nil {
		return assigneeItem{}, err
	}
	return item, nil
}

func canonicalAssigneeIDs(input []string) ([]string, error) {
	if input == nil || len(input) > MaxContentAssignees {
		return nil, ErrValidation
	}
	ids := make([]string, 0, len(input))
	for _, id := range input {
		canonical, valid := canonicalMembershipID(id)
		if !valid || slices.Contains(ids, canonical) {
			return nil, ErrValidation
		}
		ids = append(ids, canonical)
	}
	slices.Sort(ids)
	return ids, nil
}

func validateAssigneeRecipients(ctx context.Context, tx *sql.Tx, item assigneeItem, actorID string, requested []string, access accessSnapshot) error {
	rows, err := tx.QueryContext(ctx, `SELECT u.id::text, u.state, a.user_id IS NOT NULL
 FROM public.users u
 LEFT JOIN public.content_item_assignments a ON a.item_id=$1 AND a.user_id=u.id
 WHERE a.user_id IS NOT NULL OR u.id IN (SELECT id::uuid FROM unnest($2::text[]) AS requested(id))
 ORDER BY u.id`, item.id, requested)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	pending := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		pending[id] = struct{}{}
	}
	var addingActor, inactiveRequested bool
	for rows.Next() {
		var id, state string
		var existing bool
		if err := rows.Scan(&id, &state, &existing); err != nil {
			return err
		}
		// Check the union of before and after object access. Every row was or
		// will be assigned, so assigned grants belong to at least one view.
		// Latent grants include disabled roles and inactive account recipients.
		// Retaining or removing an existing self assignment cannot add access.
		addingActor = addingActor || id == actorID && !existing
		if id != actorID && !withinAssigneeEnvelope(access[actorID].active, access[id].latent, item.typeID) {
			return ErrNotFound
		}
		_, selected := pending[id]
		inactiveRequested = inactiveRequested || selected && state != userStateActive
		delete(pending, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	switch {
	case len(pending) != 0:
		return ErrNotFound
	case addingActor:
		return ErrPermissionDenied
	case inactiveRequested:
		return ErrInvalidTransition
	default:
		return nil
	}
}

func withinAssigneeEnvelope(actor, recipient []accessGrant, typeID string) bool {
	for _, grant := range recipient {
		if grant.scope == grantScopeContent && (grant.typeID != typeID || grant.objectScope == objectScopeType) {
			continue
		}
		if !withinEnvelope(actor, []accessGrant{grant}) {
			return false
		}
	}
	return true
}

func writeContentAssignments(ctx context.Context, tx *sql.Tx, itemID string, requested []string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.content_item_assignments WHERE item_id=$1
 AND user_id NOT IN (SELECT id::uuid FROM unnest($2::text[]) AS requested(id))`, itemID, requested); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_item_assignments(item_id,user_id)
 SELECT $1,id::uuid FROM unnest($2::text[]) AS requested(id) ORDER BY id
 ON CONFLICT (item_id,user_id) DO NOTHING`, itemID, requested); err != nil {
		return 0, err
	}
	var version int64
	err := tx.QueryRowContext(ctx, `UPDATE public.content_items SET version=version+1 WHERE id=$1 RETURNING version`, itemID).Scan(&version)
	return version, err
}
