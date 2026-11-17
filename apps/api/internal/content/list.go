package content

import (
	"context"
	"database/sql"
)

type itemScope struct {
	Any     bool
	ItemIDs []string
}
type scopeFunc func(context.Context, *sql.Tx, Actor, string) (itemScope, error)

// ListDrafts requires a server-derived SQL scope before pagination. No production
// scope adapter is enabled until authenticated CMS authorization exists.
func (s *Service) ListDrafts(ctx context.Context, actor Actor, typeID string, limit, offset int) ([]DraftState, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return nil, problem(Validation, "invalid_page")
	}
	if s.scope == nil {
		return nil, problem(Forbidden, "security_unavailable")
	}
	out := []DraftState{}
	err := s.withTx(ctx, actor, readOperation, typeID, "", nil, func(ctx context.Context, tx *sql.Tx) error {
		scope, err := s.scope(ctx, tx, actor, typeID)
		if err != nil {
			return err
		}
		ids, err := draftIDs(ctx, tx, typeID, scope, limit, offset)
		if err != nil {
			return err
		}
		out, err = s.materializeDraftIDs(ctx, tx, actor, typeID, ids)
		return err
	})
	return out, err
}
func draftIDs(ctx context.Context, tx *sql.Tx, typeID string, scope itemScope, limit, offset int) ([]string, error) {
	ids := []string{}
	if scope.ItemIDs == nil {
		scope.ItemIDs = []string{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM public.content_items WHERE type_id=$1 AND ($2::boolean OR id=ANY($3::uuid[])) ORDER BY id LIMIT $4 OFFSET $5`, typeID, scope.Any, scope.ItemIDs, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Service) visibleDraft(ctx context.Context, tx *sql.Tx, actor Actor, state DraftState) (DraftState, error) {
	pinned, err := loadSchema(ctx, tx, state.TypeID, state.SchemaNo)
	if err != nil {
		return DraftState{}, err
	}
	return s.projectDraft(ctx, tx, actor, state, pinned)
}

func (s *Service) materializeDraftIDs(ctx context.Context, tx *sql.Tx, actor Actor, typeID string, ids []string) ([]DraftState, error) {
	out := []DraftState{}
	for _, id := range ids {
		if err := s.authorize(ctx, tx, actor, readOperation, typeID, id, nil); err != nil {
			return nil, err
		}
		state, err := loadDraft(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		visible, err := s.visibleDraft(ctx, tx, actor, state)
		if err != nil {
			return nil, err
		}
		out = append(out, visible)
	}
	return out, nil
}
