package content

import (
	"context"
	"database/sql"
	"maps"
	"slices"
)

type relationSlot struct {
	fieldID, locale string
}

// Each submitted slot is changed intentionally, even when it already had the
// requested value. Versions and readiness do not disclose a hidden old target.
func applyDraftRelationChanges(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, changes []RelationChange, readable relationAccess) error {
	fields := make(map[string]Definition, len(schema.Fields))
	for _, field := range schema.Fields {
		fields[field.ID] = field
	}
	seen := make(map[relationSlot]bool)
	locales := make(map[string]bool)
	for _, change := range changes {
		slot := relationSlot{change.FieldID, change.Locale}
		if seen[slot] {
			return problem(Validation, "duplicate_relation_change")
		}
		seen[slot] = true
		field := fields[change.FieldID]
		if err := validateRelationChange(ctx, tx, state, field, change); err != nil {
			return err
		}
		if err := saveRelationChange(ctx, tx, state, field, change, readable); err != nil {
			return err
		}
		locales[change.Locale] = true
	}
	return invalidateChangedRelationScopes(ctx, tx, state.ItemID, locales)
}

func validateRelationChange(ctx context.Context, tx *sql.Tx, state DraftState, field Definition, change RelationChange) error {
	if field.Kind != kindRelation {
		return problem(Validation, invalidRelationCode)
	}
	if (field.Scope == scopeShared) != (change.Locale == "") {
		return problem(Validation, invalidScopeCode)
	}
	if change.Locale == "" {
		return nil
	}
	locale, err := normalizeLanguage(change.Locale)
	if err != nil {
		return err
	}
	if locale != change.Locale {
		return problem(Validation, invalidLanguageCode)
	}
	if _, exists := state.Locales[locale]; exists {
		return nil
	}
	// A scalar patch in this same command may have created the locale.
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM public.content_draft_locales WHERE item_id=$1 AND locale_code=$2)`, state.ItemID, locale).Scan(&exists)
	if err == nil && !exists {
		return problem(Validation, invalidLanguageCode)
	}
	return err
}

func saveRelationChange(ctx context.Context, tx *sql.Tx, state DraftState, field Definition, change RelationChange, readable relationAccess) error {
	if change.TargetItemID == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM public.content_draft_relations
 WHERE item_id=$1 AND field_id=$2 AND locale_scope=$3`, state.ItemID, change.FieldID, change.Locale)
		return err
	}
	target := Relation{FieldID: change.FieldID, Locale: change.Locale, TargetTypeID: field.TargetTypeID, TargetItemID: change.TargetItemID}
	allowed, err := readable(target)
	if err != nil {
		return err
	}
	if !allowed {
		return problem(Forbidden, relationEditDeniedCode)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.content_draft_relations(item_id,type_id,schema_no,field_id,locale_scope,target_type_id,target_item_id)
 VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(item_id,field_id,locale_scope) DO UPDATE SET target_item_id=EXCLUDED.target_item_id`,
		state.ItemID, state.TypeID, state.SchemaNo, change.FieldID, change.Locale, field.TargetTypeID, change.TargetItemID)
	return err
}

func invalidateChangedRelationScopes(ctx context.Context, tx *sql.Tx, itemID string, locales map[string]bool) error {
	if locales[""] {
		delete(locales, "")
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_drafts SET shared_seq=shared_seq+1,shared_ready_seq=NULL WHERE item_id=$1`, itemID); err != nil {
			return err
		}
		if err := resetReady(ctx, tx, itemID); err != nil {
			return err
		}
	}
	if len(locales) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE public.content_draft_locales
 SET local_seq=local_seq+1,ready_local_seq=NULL,ready_shared_seq=NULL,ready_schema_no=NULL,ready_locale_generation=NULL
 WHERE item_id=$1 AND locale_code=ANY($2::text[])`, itemID, slices.Collect(maps.Keys(locales)))
	return err
}
