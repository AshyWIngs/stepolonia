package content

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
)

func (s *Service) Create(ctx context.Context, actor Actor, typeID string, schemaNo int64) (DraftState, error) {
	var out DraftState
	err := s.withTx(ctx, actor, "content.items.create", typeID, "", nil, func(ctx context.Context, tx *sql.Tx) error {
		schema, err := loadSchema(ctx, tx, typeID, 0)
		if err != nil {
			return err
		}
		if schemaNo != schema.SchemaNo {
			return problem(Conflict, schemaConflictCode)
		}
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_items(id,type_id) VALUES($1,$2)`, id, typeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_drafts(item_id,type_id,schema_no) VALUES($1,$2,$3)`, id, typeID, schemaNo); err != nil {
			return err
		}
		out, err = s.commandResponse(ctx, tx, actor, id)
		return err
	})
	return out, err
}
func (s *Service) GetDraft(ctx context.Context, actor Actor, item string) (DraftState, error) {
	var out DraftState
	err := s.withTx(ctx, actor, readOperation, "", item, nil, func(ctx context.Context, tx *sql.Tx) error {
		state, err := loadDraft(ctx, tx, item)
		if err != nil {
			return err
		}
		schema, err := loadSchema(ctx, tx, state.TypeID, state.SchemaNo)
		if err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, readOperation, state.TypeID, item, nil); err != nil {
			return err
		}
		out, err = s.projectDraft(ctx, tx, actor, state, schema)
		return err
	})
	return out, err
}
func (s *Service) SaveDraft(ctx context.Context, actor Actor, item string, expected int64, patch DraftPatch) (DraftState, error) {
	var out DraftState
	err := s.withTx(ctx, actor, updateOperation, "", item, nil, func(ctx context.Context, tx *sql.Tx) error {
		state, err := loadDraft(ctx, tx, item)
		if err != nil {
			return err
		}
		if err := expectedVersion(state, expected, patch.SchemaNo); err != nil {
			return err
		}
		schema, err := loadSchema(ctx, tx, state.TypeID, state.SchemaNo)
		if err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actor, updateOperation, state.TypeID, item, patchKeys(schema, patch)); err != nil {
			return err
		}
		for _, relation := range patch.Relations {
			if err := s.authorizeRelations(ctx, tx, actor, schema, []Relation{relation}, relation.Locale, false); err != nil {
				return err
			}
		}
		if err := applyDraftPatch(ctx, tx, &state, schema, patch); err != nil {
			return err
		}
		out, err = s.commandResponse(ctx, tx, actor, item)
		return err
	})
	return out, err
}
func patchKeys(schema TypeSchema, patch DraftPatch) []string {
	keys := append([]string{}, patch.UnsetShared...)
	for key := range patch.Shared {
		keys = append(keys, key)
	}
	for _, locale := range patch.Locales {
		keys = append(keys, locale.Unset...)
		for key := range locale.Values {
			keys = append(keys, key)
		}
	}
	if patch.ReplaceRelations {
		for _, field := range schema.Fields {
			if field.Kind == kindRelation {
				keys = append(keys, field.Key)
			}
		}
	}
	return keys
}
func patchValues(schema TypeSchema, scope string, current ScalarValues, patch ValuePatch) (ScalarValues, bool, error) {
	values := ScalarValues{}
	for key, value := range current {
		values[key] = value
	}
	fields := map[string]Definition{}
	for _, field := range schema.Fields {
		fields[field.Key] = field
	}
	for _, key := range patch.Unset {
		field, ok := fields[key]
		if !ok || field.Scope != scope || field.Kind == kindRelation {
			return nil, false, problem(Validation, invalidFieldCode)
		}
		if _, exists := patch.Values[key]; exists {
			return nil, false, problem(Validation, "ambiguous_patch")
		}
		delete(values, key)
	}
	for key, value := range patch.Values {
		field, ok := fields[key]
		if !ok || field.Scope != scope {
			return nil, false, problem(Validation, invalidFieldCode)
		}
		scalar, err := validateValue(field, value)
		if err != nil {
			return nil, false, err
		}
		values[key] = scalar
	}
	if err := validateValues(schema, scope, values, false); err != nil {
		return nil, false, err
	}
	before, _ := json.Marshal(current)
	after, _ := json.Marshal(values)
	return values, !bytes.Equal(before, after), nil
}
func applyDraftPatch(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema, patch DraftPatch) error {
	values, sharedChanged, err := patchValues(schema, scopeShared, state.Shared, ValuePatch{patch.Shared, patch.UnsetShared})
	if err != nil {
		return err
	}
	if sharedChanged {
		raw, _ := json.Marshal(values)
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_drafts SET shared_values=$2,shared_seq=shared_seq+1,shared_ready_seq=NULL WHERE item_id=$1`, state.ItemID, raw); err != nil {
			return err
		}
		if err := resetReady(ctx, tx, state.ItemID); err != nil {
			return err
		}
	}
	changed := sharedChanged
	localeChanged, err := saveLocalePatches(ctx, tx, *state, schema, patch.Locales)
	if err != nil {
		return err
	}
	changed = changed || localeChanged
	if patch.ReplaceRelations {
		fresh, err := loadDraft(ctx, tx, state.ItemID)
		if err != nil {
			return err
		}
		relationsChanged, err := replaceRelations(ctx, tx, fresh, schema, patch.Relations)
		if err != nil {
			return err
		}
		changed = changed || relationsChanged
	}
	if changed {
		return advanceVersion(ctx, tx, state)
	}
	return nil
}
func saveLocalePatch(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, locale string, patch ValuePatch) (bool, error) {
	if _, err := normalizeLanguage(locale); err != nil {
		return false, err
	}
	current, exists := state.Locales[locale]
	if !exists {
		current.Values = ScalarValues{}
	}
	values, changed, err := patchValues(schema, scopeLocalized, current.Values, patch)
	if err != nil {
		return false, err
	}
	if !changed && exists {
		return false, nil
	}
	raw, _ := json.Marshal(values)
	_, err = tx.ExecContext(ctx, `INSERT INTO public.content_draft_locales(item_id,locale_code,scalar_values) VALUES($1,$2,$3) ON CONFLICT(item_id,locale_code) DO UPDATE SET scalar_values=EXCLUDED.scalar_values,local_seq=content_draft_locales.local_seq+1,ready_local_seq=NULL,ready_shared_seq=NULL,ready_schema_no=NULL,ready_locale_generation=NULL`, state.ItemID, locale, raw)
	return true, err
}
func (s *Service) AdoptSchema(ctx context.Context, actor Actor, item string, expected, current int64) (DraftState, error) {
	var out DraftState
	err := s.withTx(ctx, actor, updateOperation, "", item, nil, func(ctx context.Context, tx *sql.Tx) error {
		state, err := loadDraft(ctx, tx, item)
		if err != nil {
			return err
		}
		if expected != state.Version {
			return problem(Conflict, versionConflictCode)
		}
		schema, err := loadSchema(ctx, tx, state.TypeID, 0)
		if err != nil {
			return err
		}
		if schema.SchemaNo != current {
			return problem(Conflict, schemaConflictCode)
		}
		if err := s.authorize(ctx, tx, actor, updateOperation, state.TypeID, item, fieldKeys(schema)); err != nil {
			return err
		}
		if current == state.SchemaNo {
			out, err = s.visibleDraft(ctx, tx, actor, state)
			return err
		}
		if err := adoptDraftSchema(ctx, tx, &state, current); err != nil {
			return err
		}
		out, err = s.commandResponse(ctx, tx, actor, item)
		return err
	})
	return out, err
}

func saveLocalePatches(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, patches map[string]ValuePatch) (bool, error) {
	changed := false
	for locale, patch := range patches {
		localeChanged, err := saveLocalePatch(ctx, tx, state, schema, locale, patch)
		if err != nil {
			return false, err
		}
		changed = changed || localeChanged
	}
	return changed, nil
}

func adoptDraftSchema(ctx context.Context, tx *sql.Tx, state *DraftState, current int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.content_drafts SET schema_no=$2 WHERE item_id=$1`, state.ItemID, current); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.content_draft_relations SET schema_no=$2 WHERE item_id=$1`, state.ItemID, current); err != nil {
		return err
	}
	if err := resetReady(ctx, tx, state.ItemID); err != nil {
		return err
	}
	if err := advanceVersion(ctx, tx, state); err != nil {
		return err
	}
	return nil
}
