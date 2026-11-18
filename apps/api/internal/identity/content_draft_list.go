package identity

import (
	"context"
	"database/sql"
	"errors"
)

const (
	DefaultDraftPageLimit = 20
	MaxDraftPageLimit     = 100
	MaxDraftPageOffset    = 10000
)

type DraftSummary struct {
	ItemID   string `json:"item_id"`
	TypeID   string `json:"type_id"`
	SchemaNo int64  `json:"schema_no,string"`
	Version  int64  `json:"version,string"`
}

type DraftPage struct {
	Items      []DraftSummary `json:"items"`
	NextOffset *int           `json:"next_offset"`
}

// ReadDraftPage loads only draft identity and versions, after applying the
// actor's current item scope and before pagination.
func (s *AuthService) ReadDraftPage(ctx context.Context, req AuthRequest, typeKey string, limit, offset int) (DraftPage, error) {
	if limit < 1 || limit > MaxDraftPageLimit || offset < 0 || offset > MaxDraftPageOffset {
		return DraftPage{}, ErrValidation
	}
	var page DraftPage
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		typeID, anyItem, err := draftListScope(ctx, tx, session.user, typeKey)
		if err != nil {
			return err
		}
		page, err = readDraftPage(ctx, tx, session.user, typeID, anyItem, limit, offset)
		return err
	})
	if err != nil {
		return DraftPage{}, err
	}
	return page, nil
}

func draftListScope(ctx context.Context, tx *sql.Tx, userID, typeKey string) (string, bool, error) {
	var typeID string
	var anyItem bool
	err := tx.QueryRowContext(ctx, `SELECT t.id::text, bool_or(g.object_scope='any')
 FROM public.content_types t
 JOIN public.role_grants g ON g.type_id=t.id
 JOIN public.permission_operations o ON o.key=g.operation AND o.scope='content'
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$1
 WHERE t.key=$2 AND g.operation=$3 AND g.scope='content' AND g.object_scope IN ('any','assigned')
 GROUP BY t.id`, userID, typeKey, OpContentRead).Scan(&typeID, &anyItem)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return typeID, anyItem, err
}

func readDraftPage(ctx context.Context, tx *sql.Tx, userID, typeID string, anyItem bool, limit, offset int) (DraftPage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.id::text, d.schema_no, i.version
 FROM public.content_items i
 JOIN public.content_drafts d ON d.item_id=i.id AND d.type_id=i.type_id
 WHERE i.type_id=$1 AND ($2::boolean OR EXISTS (
 SELECT 1 FROM public.content_item_assignments a WHERE a.item_id=i.id AND a.user_id=$3))
 ORDER BY i.id LIMIT $4 OFFSET $5`, typeID, anyItem, userID, limit+1, offset)
	if err != nil {
		return DraftPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := DraftPage{Items: make([]DraftSummary, 0, limit)}
	for rows.Next() {
		var item DraftSummary
		item.TypeID = typeID
		if err := rows.Scan(&item.ItemID, &item.SchemaNo, &item.Version); err != nil {
			return DraftPage{}, err
		}
		if len(page.Items) == limit {
			next := offset + limit
			if next <= MaxDraftPageOffset {
				page.NextOffset = &next
			}
			break
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return DraftPage{}, err
	}
	return page, nil
}
