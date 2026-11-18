package content

import (
	"context"
	"database/sql"
)

func (s *Service) withCandidate(ctx context.Context, actor Actor, operation, item string, expected, schemaNo int64, command func(context.Context, *sql.Tx, *DraftState, TypeSchema) error) (DraftState, error) {
	var out DraftState
	err := s.withTx(ctx, actor, operation, "", item, nil, func(ctx context.Context, tx *sql.Tx) error {
		state, err := loadDraft(ctx, tx, item)
		if err != nil {
			return err
		}
		if err := validateDraftVersions(state, expected, schemaNo); err != nil {
			return err
		}
		schema, err := currentSchema(ctx, tx, state)
		if err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, operation, state.TypeID, item, fieldKeys(schema)); err != nil {
			return err
		}
		if err := command(ctx, tx, &state, schema); err != nil {
			return err
		}
		out, err = s.commandResponse(ctx, tx, actor, item)
		return err
	})
	return out, err
}
func (s *Service) MarkSharedReady(ctx context.Context, actor Actor, item string, expected, schemaNo int64) (DraftState, error) {
	return s.withCandidate(ctx, actor, "content.items.ready", item, expected, schemaNo, func(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema) error {
		if err := validateValues(schema, scopeShared, state.Shared, true); err != nil {
			return err
		}
		if err := validateRelations(schema, *state, state.Relations); err != nil {
			return err
		}
		if err := s.authorizeRelations(ctx, tx, actor, schema, scopeRelations(state.Relations, ""), "", false); err != nil {
			return err
		}
		if state.SharedReadySeq.Valid && state.SharedReadySeq.Int64 == state.SharedSeq {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_drafts SET shared_ready_seq=shared_seq WHERE item_id=$1`, state.ItemID); err != nil {
			return err
		}
		return advanceVersion(ctx, tx, state)
	})
}
func (s *Service) MarkReady(ctx context.Context, actor Actor, item, locale string, expected, schemaNo int64) (DraftState, error) {
	return s.withCandidate(ctx, actor, "content.items.ready", item, expected, schemaNo, func(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema) error {
		generation, err := languageGeneration(ctx, tx, locale)
		if err != nil {
			return err
		}
		local, ok := state.Locales[locale]
		if !ok {
			return problem(NotFound, "not_found")
		}
		if err := validateLocaleCandidate(schema, *state, local); err != nil {
			return err
		}
		if err := s.authorizeRelations(ctx, tx, actor, schema, state.Relations, locale, false); err != nil {
			return err
		}
		ready := ReadyTuple{local.LocalSeq, state.SharedSeq, state.SchemaNo, generation}
		if local.Ready != nil && *local.Ready == ready {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_draft_locales SET ready_local_seq=local_seq,ready_shared_seq=$3,ready_schema_no=$4,ready_locale_generation=$5 WHERE item_id=$1 AND locale_code=$2`, state.ItemID, locale, state.SharedSeq, state.SchemaNo, generation); err != nil {
			return err
		}
		return advanceVersion(ctx, tx, state)
	})
}
func (s *Service) Unpublish(ctx context.Context, actor Actor, item string, expected, schemaNo int64) (DraftState, error) {
	var out DraftState
	err := s.withTx(ctx, actor, "content.items.unpublish", "", item, nil, func(ctx context.Context, tx *sql.Tx) error {
		state, err := loadDraft(ctx, tx, item)
		if err != nil {
			return err
		}
		if err := validateDraftVersions(state, expected, schemaNo); err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, "content.items.unpublish", state.TypeID, item, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_items SET published_id=NULL WHERE id=$1`, item); err != nil {
			return err
		}
		if err := resetReady(ctx, tx, item); err != nil {
			return err
		}
		if err := advanceVersion(ctx, tx, &state); err != nil {
			return err
		}
		out, err = s.commandResponse(ctx, tx, actor, item)
		return err
	})
	return out, err
}

func validateLocaleCandidate(schema TypeSchema, state DraftState, local LocaleDraft) error {
	if err := validateValues(schema, scopeShared, state.Shared, true); err != nil {
		return err
	}
	if err := validateValues(schema, scopeLocalized, local.Values, true); err != nil {
		return err
	}
	if err := validateRelations(schema, state, state.Relations); err != nil {
		return err
	}
	return nil
}
