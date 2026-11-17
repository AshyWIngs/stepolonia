package content

import (
	"context"
	"database/sql"
	"errors"
	"stepolonia/apps/api/internal/cms"
)

func (s *Service) projectDraft(ctx context.Context, tx *sql.Tx, actor Actor, state DraftState, schema TypeSchema) (DraftState, error) {
	allowed := map[string]bool{}
	ids := map[string]bool{}
	for _, field := range schema.Fields {
		err := s.authorize(ctx, tx, actor, string(cms.OpContentRead), state.TypeID, state.ItemID, []string{field.Key})
		var denied *Error
		if errors.As(err, &denied) && denied.Kind == Forbidden {
			continue
		}
		if err != nil {
			return DraftState{}, err
		}
		allowed[field.Key] = true
		ids[field.ID] = true
	}
	state.Shared = projectValues(state.Shared, allowed)
	for locale, local := range state.Locales {
		local.Values = projectValues(local.Values, allowed)
		state.Locales[locale] = local
	}
	relations := []Relation{}
	for _, relation := range state.Relations {
		if !ids[relation.FieldID] {
			continue
		}
		visible, err := s.visibleRelation(ctx, tx, actor, relation)
		if err != nil {
			return DraftState{}, err
		}
		if visible {
			relations = append(relations, relation)
		}
	}
	state.Relations = relations
	return state, nil
}
func projectValues(values ScalarValues, allowed map[string]bool) ScalarValues {
	out := ScalarValues{}
	for key, value := range values {
		if allowed[key] {
			out[key] = value
		}
	}
	return out
}

func (s *Service) visibleRelation(ctx context.Context, tx *sql.Tx, actor Actor, relation Relation) (bool, error) {
	err := s.authorize(ctx, tx, actor, string(cms.OpContentRead), relation.TargetTypeID, relation.TargetItemID, nil)
	var denied *Error
	if errors.As(err, &denied) && (denied.Kind == Forbidden || denied.Kind == NotFound) {
		return false, nil
	}
	return err == nil, err
}

// commandResponse materializes only actor-readable fields before the transaction commits.
func (s *Service) commandResponse(ctx context.Context, tx *sql.Tx, actor Actor, item string) (DraftState, error) {
	state, err := loadDraft(ctx, tx, item)
	if err != nil {
		return DraftState{}, err
	}
	return s.visibleDraft(ctx, tx, actor, state)
}
