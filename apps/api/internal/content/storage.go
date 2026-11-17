package content

import (
	"context"
	"database/sql"
	"encoding/json"
)

func loadDraft(ctx context.Context, tx *sql.Tx, item string) (DraftState, error) {
	state := DraftState{Locales: map[string]LocaleDraft{}, Shared: ScalarValues{}}
	var shared []byte
	err := tx.QueryRowContext(ctx, `SELECT i.id::text,i.type_id::text,i.version,COALESCE(i.published_id::text,''),d.schema_no,d.shared_values,d.shared_seq,d.shared_ready_seq FROM public.content_items i JOIN public.content_drafts d ON d.item_id=i.id WHERE i.id=$1 FOR UPDATE OF i`, item).Scan(&state.ItemID, &state.TypeID, &state.Version, &state.PublicationID, &state.SchemaNo, &shared, &state.SharedSeq, &state.SharedReadySeq)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(shared, &state.Shared); err != nil {
		return state, err
	}
	if err := loadDraftLocales(ctx, tx, &state); err != nil {
		return state, err
	}
	relations, err := loadRelations(ctx, tx, item, "")
	state.Relations = relations
	return state, err
}
func loadDraftLocales(ctx context.Context, tx *sql.Tx, state *DraftState) error {
	rows, err := tx.QueryContext(ctx, `SELECT locale_code,scalar_values,local_seq,ready_local_seq,ready_shared_seq,ready_schema_no,ready_locale_generation FROM public.content_draft_locales WHERE item_id=$1 ORDER BY locale_code`, state.ItemID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var locale string
		var raw []byte
		var draft LocaleDraft
		var local, shared, schema, generation sql.NullInt64
		if err := rows.Scan(&locale, &raw, &draft.LocalSeq, &local, &shared, &schema, &generation); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &draft.Values); err != nil {
			return err
		}
		if local.Valid {
			draft.Ready = &ReadyTuple{local.Int64, shared.Int64, schema.Int64, generation.Int64}
		}
		state.Locales[locale] = draft
	}
	return rows.Err()
}
func loadRelations(ctx context.Context, tx *sql.Tx, item, publication string) ([]Relation, error) {
	query := `SELECT field_id::text,locale_scope,target_type_id::text,target_item_id::text FROM public.content_draft_relations WHERE item_id=$1 ORDER BY field_id,locale_scope`
	arg := item
	if publication != "" {
		query = `SELECT field_id::text,locale_scope,target_type_id::text,target_item_id::text FROM public.content_publication_relations WHERE publication_id=$1 ORDER BY field_id,locale_scope`
		arg = publication
	}
	rows, err := tx.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Relation{}
	for rows.Next() {
		var relation Relation
		if err := rows.Scan(&relation.FieldID, &relation.Locale, &relation.TargetTypeID, &relation.TargetItemID); err != nil {
			return nil, err
		}
		out = append(out, relation)
	}
	return out, rows.Err()
}
func advanceVersion(ctx context.Context, tx *sql.Tx, state *DraftState) error {
	return tx.QueryRowContext(ctx, `UPDATE public.content_items SET version=version+1 WHERE id=$1 RETURNING version`, state.ItemID).Scan(&state.Version)
}
func expectedVersion(state DraftState, expected, schema int64) error {
	if expected <= 0 || schema <= 0 {
		return problem(Validation, "expected_version_required")
	}
	if expected != state.Version {
		return problem(Conflict, versionConflictCode)
	}
	if schema != state.SchemaNo {
		return problem(Conflict, schemaConflictCode)
	}
	return nil
}
func currentSchema(ctx context.Context, tx *sql.Tx, state DraftState) (TypeSchema, error) {
	schema, err := loadSchema(ctx, tx, state.TypeID, 0)
	if err != nil {
		return schema, err
	}
	if schema.SchemaNo != state.SchemaNo {
		return schema, problem(Conflict, schemaConflictCode)
	}
	return schema, nil
}
func languageGeneration(ctx context.Context, tx *sql.Tx, locale string) (int64, error) {
	if _, err := normalizeLanguage(locale); err != nil {
		return 0, err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM public.languages WHERE code=$1 AND is_enabled`, locale).Scan(&generation)
	if err == sql.ErrNoRows {
		return 0, problem(Conflict, "locale_unavailable")
	}
	return generation, err
}
func resetReady(ctx context.Context, tx *sql.Tx, item string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.content_drafts SET shared_ready_seq=NULL WHERE item_id=$1`, item); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE public.content_draft_locales SET ready_local_seq=NULL,ready_shared_seq=NULL,ready_schema_no=NULL,ready_locale_generation=NULL WHERE item_id=$1`, item)
	return err
}
