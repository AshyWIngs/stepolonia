package content

import (
	"context"
	"database/sql"
	"encoding/json"
)

type publicationLocale struct {
	Locale     string
	Values     ScalarValues
	Generation int64
	Slug       string
	Relations  []Relation
}
type publicationCandidate struct {
	Shared            ScalarValues
	Locales           map[string]publicationLocale
	Relations         []Relation
	PreviousSharedSeq int64
	PreviousSchema    int64
}

func (s *Service) Publish(ctx context.Context, actor Actor, item string, expected, schemaNo int64, intended []string) (DraftState, error) {
	return s.withCandidate(ctx, actor, "content.items.publish", item, expected, schemaNo, func(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema) error {
		candidate, err := publicationPlan(ctx, tx, *state, schema, intended)
		if err != nil {
			return err
		}
		for locale, local := range candidate.Locales {
			relations := append(append([]Relation{}, candidate.Relations...), local.Relations...)
			if err := s.authorizeRelations(ctx, tx, actor, schema, relations, locale, true); err != nil {
				return err
			}
		}
		id, err := storePublication(ctx, tx, *state, schema, candidate)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_items SET published_id=$2 WHERE id=$1`, item, id); err != nil {
			return err
		}
		return advanceVersion(ctx, tx, state)
	})
}
func publicationPlan(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, intended []string) (publicationCandidate, error) {
	candidate, err := previousPublication(ctx, tx, state)
	if err != nil {
		return candidate, err
	}
	if len(intended) == 0 || len(intended) > 20 {
		return candidate, problem(Validation, "invalid_locales")
	}
	if !state.SharedReadySeq.Valid || state.SharedReadySeq.Int64 != state.SharedSeq {
		return candidate, problem(Conflict, notReadyCode)
	}
	if err := validateValues(schema, scopeShared, state.Shared, true); err != nil {
		return candidate, err
	}
	if err := mergeIntendedLocales(ctx, tx, state, schema, intended, &candidate); err != nil {
		return candidate, err
	}
	candidate.Shared = state.Shared
	candidate.Relations = scopeRelations(state.Relations, "")
	return candidate, nil
}
func readyLocale(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, locale string) (publicationLocale, error) {
	generation, err := languageGeneration(ctx, tx, locale)
	if err != nil {
		return publicationLocale{}, err
	}
	draft, ok := state.Locales[locale]
	if !ok || draft.Ready == nil {
		return publicationLocale{}, problem(Conflict, notReadyCode)
	}
	expected := ReadyTuple{draft.LocalSeq, state.SharedSeq, state.SchemaNo, generation}
	if *draft.Ready != expected {
		return publicationLocale{}, problem(Conflict, notReadyCode)
	}
	if err := validateValues(schema, scopeLocalized, draft.Values, true); err != nil {
		return publicationLocale{}, err
	}
	var slug string
	if err := json.Unmarshal(draft.Values[slugFieldKey], &slug); err != nil || !slugPattern.MatchString(slug) || len(slug) > 200 {
		return publicationLocale{}, problem(Validation, invalidSlugCode)
	}
	return publicationLocale{locale, draft.Values, generation, slug, scopeRelations(state.Relations, locale)}, nil
}
func previousPublication(ctx context.Context, tx *sql.Tx, state DraftState) (publicationCandidate, error) {
	candidate := publicationCandidate{Locales: map[string]publicationLocale{}}
	if state.PublicationID == "" {
		return candidate, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT shared_seq,schema_no FROM public.content_publications WHERE id=$1 AND item_id=$2 AND sealed`, state.PublicationID, state.ItemID).Scan(&candidate.PreviousSharedSeq, &candidate.PreviousSchema); err != nil {
		return candidate, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT l.locale_code,l.scalar_values,l.locale_generation,l.slug FROM public.content_publication_locales l JOIN public.languages g ON g.code=l.locale_code WHERE l.publication_id=$1 AND g.is_enabled AND g.generation=l.locale_generation`, state.PublicationID)
	if err != nil {
		return candidate, err
	}
	for rows.Next() {
		var local publicationLocale
		var raw []byte
		if err := rows.Scan(&local.Locale, &raw, &local.Generation, &local.Slug); err != nil {
			_ = rows.Close()
			return candidate, err
		}
		if err := json.Unmarshal(raw, &local.Values); err != nil {
			_ = rows.Close()
			return candidate, err
		}
		candidate.Locales[local.Locale] = local
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return candidate, err
	}
	if err := rows.Close(); err != nil {
		return candidate, err
	}
	relations, err := loadRelations(ctx, tx, state.ItemID, state.PublicationID)
	if err != nil {
		return candidate, err
	}
	for locale, local := range candidate.Locales {
		local.Relations = scopeRelations(relations, locale)
		candidate.Locales[locale] = local
	}
	return candidate, nil
}
func storePublication(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, candidate publicationCandidate) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	shared, err := json.Marshal(candidate.Shared)
	if err != nil {
		return "", err
	}
	metadata := map[string]json.RawMessage{}
	for _, field := range schema.Fields {
		metadata[field.Key] = defaultObject(field.DisplayLabels)
	}
	labels, err := json.Marshal(metadata)
	if err != nil || len(labels) > 1024*1024 {
		return "", problem(Validation, "metadata_limit")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_publications(id,item_id,type_id,schema_no,revision_no,shared_seq,shared_values,display_metadata) SELECT $1,$2,$3,$4,COALESCE(max(revision_no),0)+1,$5,$6,$7 FROM public.content_publications WHERE item_id=$2`, id, state.ItemID, state.TypeID, state.SchemaNo, state.SharedSeq, shared, labels); err != nil {
		return "", err
	}
	for _, local := range candidate.Locales {
		if err := storePublicationLocale(ctx, tx, state, schema, id, local); err != nil {
			return "", err
		}
	}
	if err := storePublicationRelations(ctx, tx, state, id, candidate.Relations); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.content_publications SET sealed=true WHERE id=$1`, id); err != nil {
		return "", err
	}
	return id, nil
}
func storePublicationLocale(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, id string, local publicationLocale) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_routes(namespace,locale_code,slug,type_id,item_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(namespace,locale_code,slug) DO NOTHING`, schema.RouteNamespace, local.Locale, local.Slug, state.TypeID, state.ItemID); err != nil {
		return err
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT item_id::text FROM public.content_routes WHERE namespace=$1 AND locale_code=$2 AND slug=$3`, schema.RouteNamespace, local.Locale, local.Slug).Scan(&owner); err != nil {
		return err
	}
	if owner != state.ItemID {
		return problem(Conflict, "route_conflict")
	}
	values, err := json.Marshal(local.Values)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_publication_locales(publication_id,item_id,locale_code,scalar_values,locale_generation,slug) VALUES($1,$2,$3,$4,$5,$6)`, id, state.ItemID, local.Locale, values, local.Generation, local.Slug); err != nil {
		return err
	}
	return storePublicationRelations(ctx, tx, state, id, local.Relations)
}
func storePublicationRelations(ctx context.Context, tx *sql.Tx, state DraftState, id string, relations []Relation) error {
	for _, relation := range relations {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_publication_relations(publication_id,item_id,type_id,schema_no,field_id,locale_scope,target_type_id,target_item_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, state.ItemID, state.TypeID, state.SchemaNo, relation.FieldID, relation.Locale, relation.TargetTypeID, relation.TargetItemID); err != nil {
			return err
		}
	}
	return nil
}

func mergeIntendedLocales(ctx context.Context, tx *sql.Tx, state DraftState, schema TypeSchema, intended []string, candidate *publicationCandidate) error {
	seen := map[string]bool{}
	for _, locale := range intended {
		if seen[locale] {
			return problem(Validation, "invalid_locales")
		}
		seen[locale] = true
		local, err := readyLocale(ctx, tx, state, schema, locale)
		if err != nil {
			return err
		}
		candidate.Locales[locale] = local
	}
	for locale := range candidate.Locales {
		if !seen[locale] && (candidate.PreviousSharedSeq != state.SharedSeq || candidate.PreviousSchema != state.SchemaNo) {
			return problem(Conflict, notReadyCode)
		}
	}
	return nil
}
