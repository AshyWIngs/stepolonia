package identity

import (
	"context"
	"database/sql"
)

type ReadableCollection struct {
	TypeID      string `json:"type_id"`
	TypeKey     string `json:"type_key"`
	DisplayName string `json:"display_name"`
	IconSVG     string `json:"icon_svg"`
	SchemaNo    int64  `json:"schema_no,string"`
	CanCreate   bool   `json:"can_create"`
}

type ReadableCollectionPage struct {
	Items      []ReadableCollection `json:"items"`
	NextOffset *int                 `json:"next_offset"`
}

// ListReadableCollections returns a bounded page of collections with a current
// read grant. The draft list applies the actor's item assignments separately.
func (s *AuthService) ListReadableCollections(ctx context.Context, req AuthRequest, limit, offset int) (ReadableCollectionPage, error) {
	if !validDraftPage(limit, offset) {
		return ReadableCollectionPage{}, ErrValidation
	}
	var page ReadableCollectionPage
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		var err error
		page, err = readCollectionPage(ctx, tx, session.user, limit, offset)
		return err
	})
	if err != nil {
		return ReadableCollectionPage{}, err
	}
	return page, nil
}

func readCollectionPage(ctx context.Context, tx *sql.Tx, userID string, limit, offset int) (ReadableCollectionPage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id::text, t.key, t.display_name, t.icon_svg, t.current_schema_no, grants.can_create
 FROM public.content_types t
 JOIN public.content_schemas s ON s.type_id=t.id AND s.schema_no=t.current_schema_no AND s.sealed
 CROSS JOIN LATERAL (
 SELECT bool_or(g.operation=$2 AND g.object_scope IN ('any','assigned')) AS can_read,
        bool_or(g.operation=$3 AND g.object_scope='type') AS can_create
 FROM public.role_grants g
 JOIN public.permission_operations o ON o.key=g.operation AND o.scope='content'
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$1
 WHERE g.type_id=t.id AND g.operation IN ($2,$3) AND g.scope='content') AS grants
 WHERE t.enabled AND grants.can_read
 ORDER BY t.key LIMIT $4 OFFSET $5`, userID, OpContentRead, opContentCreate, limit+1, offset)
	if err != nil {
		return ReadableCollectionPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := ReadableCollectionPage{Items: make([]ReadableCollection, 0, limit)}
	for rows.Next() {
		if len(page.Items) == limit {
			page.NextOffset = nextDraftPageOffset(limit, offset)
			break
		}
		var item ReadableCollection
		if err := rows.Scan(&item.TypeID, &item.TypeKey, &item.DisplayName, &item.IconSVG, &item.SchemaNo, &item.CanCreate); err != nil {
			return ReadableCollectionPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ReadableCollectionPage{}, err
	}
	return page, nil
}
