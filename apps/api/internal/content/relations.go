package content

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"stepolonia/apps/api/internal/cms"
)

func replaceRelations(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, relations []Relation) (bool, error) {
	if err := validateRelations(schema, state, relations); err != nil {
		return false, err
	}
	before := append([]Relation{}, state.Relations...)
	after := append([]Relation{}, relations...)
	sortRelations(before)
	sortRelations(after)
	if reflect.DeepEqual(before, after) {
		return false, nil
	}
	sharedChanged := !reflect.DeepEqual(scopeRelations(before, ""), scopeRelations(after, ""))
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.content_draft_relations WHERE item_id=$1`, state.ItemID); err != nil {
		return false, err
	}
	for _, relation := range relations {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_draft_relations(item_id,type_id,schema_no,field_id,locale_scope,target_type_id,target_item_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, state.ItemID, state.TypeID, state.SchemaNo, relation.FieldID, relation.Locale, relation.TargetTypeID, relation.TargetItemID); err != nil {
			return false, err
		}
	}
	if sharedChanged {
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_drafts SET shared_seq=shared_seq+1 WHERE item_id=$1`, state.ItemID); err != nil {
			return false, err
		}
		return true, resetReady(ctx, tx, state.ItemID)
	}
	return true, invalidateRelationLocales(ctx, tx, state.ItemID, before, after)
}
func scopeRelations(relations []Relation, locale string) []Relation {
	out := []Relation{}
	for _, relation := range relations {
		if relation.Locale == locale {
			out = append(out, relation)
		}
	}
	return out
}
func sortRelations(relations []Relation) {
	sort.Slice(relations, func(i, j int) bool {
		a, b := relations[i], relations[j]
		if a.Locale != b.Locale {
			return a.Locale < b.Locale
		}
		return a.FieldID < b.FieldID
	})
}
func validateRelations(schema TypeSchema, state DraftState, relations []Relation) error {
	fields := map[string]Definition{}
	for _, field := range schema.Fields {
		fields[field.ID] = field
	}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, relation := range relations {
		if err := validateRelation(fields, state, relation); err != nil {
			return err
		}
		key := relation.FieldID + "/" + relation.Locale
		counts[relation.Locale]++
		if seen[key] || counts[relation.Locale] > 64 {
			return problem(Validation, "relations_limit")
		}
		seen[key] = true
	}
	return nil
}
func invalidateRelationLocales(ctx context.Context, tx *sql.Tx, item string, before, after []Relation) error {
	locales := map[string]bool{}
	for _, relation := range append(append([]Relation{}, before...), after...) {
		if relation.Locale != "" {
			locales[relation.Locale] = true
		}
	}
	for locale := range locales {
		if reflect.DeepEqual(scopeRelations(before, locale), scopeRelations(after, locale)) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_draft_locales SET local_seq=local_seq+1,ready_local_seq=NULL,ready_shared_seq=NULL,ready_schema_no=NULL,ready_locale_generation=NULL WHERE item_id=$1 AND locale_code=$2`, item, locale); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) authorizeRelations(ctx context.Context, tx *sql.Tx, actor Actor, schema TypeSchema, relations []Relation, locale string, public bool) error {
	fields := map[string]Definition{}
	for _, field := range schema.Fields {
		fields[field.ID] = field
	}
	for _, relation := range relations {
		if relation.Locale != "" && relation.Locale != locale {
			continue
		}
		if err := s.authorize(ctx, tx, actor, string(cms.OpContentRead), relation.TargetTypeID, relation.TargetItemID, nil); err != nil {
			return problem(Conflict, "relation_unavailable")
		}
		field := fields[relation.FieldID]
		if !public || field.Visibility != visibilityPublic {
			continue
		}
		var available bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.content_items i JOIN public.content_publications p ON p.id=i.published_id AND p.item_id=i.id JOIN public.content_publication_locales l ON l.publication_id=p.id JOIN public.languages g ON g.code=l.locale_code WHERE i.id=$1 AND i.type_id=$2 AND p.sealed AND l.locale_code=$3 AND g.is_enabled AND g.generation=l.locale_generation)`, relation.TargetItemID, relation.TargetTypeID, locale).Scan(&available); err != nil {
			return err
		}
		if !available {
			return problem(Conflict, "relation_unavailable")
		}
	}
	return nil
}

func validateRelation(fields map[string]Definition, state DraftState, relation Relation) error {
	field, ok := fields[relation.FieldID]
	if !ok || field.Kind != kindRelation || field.TargetTypeID != relation.TargetTypeID || relation.TargetItemID == "" {
		return problem(Validation, "invalid_relation")
	}
	if field.Scope == scopeShared && relation.Locale != "" || field.Scope == scopeLocalized && relation.Locale == "" {
		return problem(Validation, "invalid_scope")
	}
	if relation.Locale != "" {
		if _, ok := state.Locales[relation.Locale]; !ok {
			return problem(Validation, invalidLanguageCode)
		}
	}
	return nil
}
