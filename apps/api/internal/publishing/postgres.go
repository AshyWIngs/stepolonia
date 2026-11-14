package publishing

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

func lockArticle(ctx context.Context, tx *sql.Tx, id string) (ArticleState, error) {
	var state ArticleState
	err := tx.QueryRowContext(ctx, "SELECT id,version,published_revision_id FROM public.articles WHERE id=$1 FOR UPDATE", id).Scan(&state.ID, &state.Version, &state.PublishedRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ArticleState{}, problem(NotFound, articleNotFoundCode)
	}
	return state, databaseError(err)
}

func advanceVersion(ctx context.Context, tx *sql.Tx, state *ArticleState) error {
	return databaseError(tx.QueryRowContext(ctx, "UPDATE public.articles SET version=version+1,published_revision_id=$2 WHERE id=$1 RETURNING version", state.ID, state.PublishedRevisionID).Scan(&state.Version))
}

func savePatch(ctx context.Context, tx *sql.Tx, id string, patch map[string]TranslationPatch) (bool, error) {
	codes := make([]string, 0, len(patch))
	for code := range patch {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	changed := false
	for _, code := range codes {
		fields := patch[code]
		result, err := tx.ExecContext(ctx, `INSERT INTO public.article_draft_translations AS draft
 (article_id,language_code,title,slug,body_markdown) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(article_id,language_code) DO UPDATE SET
 title=CASE WHEN $6 THEN EXCLUDED.title ELSE draft.title END,
 slug=CASE WHEN $7 THEN EXCLUDED.slug ELSE draft.slug END,
 body_markdown=CASE WHEN $8 THEN EXCLUDED.body_markdown ELSE draft.body_markdown END
 WHERE (draft.title,draft.slug,draft.body_markdown) IS DISTINCT FROM
 (CASE WHEN $6 THEN EXCLUDED.title ELSE draft.title END,
 CASE WHEN $7 THEN EXCLUDED.slug ELSE draft.slug END,
 CASE WHEN $8 THEN EXCLUDED.body_markdown ELSE draft.body_markdown END)`,
			id, code, fields.Title.Value, fields.Slug.Value, fields.BodyMarkdown.Value,
			fields.Title.Set, fields.Slug.Set, fields.BodyMarkdown.Set)
		if err != nil {
			return false, databaseError(err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return false, databaseError(err)
		}
		changed = changed || count != 0
	}
	return changed, nil
}

func requireDraft(ctx context.Context, tx *sql.Tx, id, code string) error {
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM public.article_draft_translations WHERE article_id=$1 AND language_code=$2)", id, code).Scan(&exists)
	if err != nil {
		return databaseError(err)
	}
	if !exists {
		return problem(NotFound, "translation_not_found")
	}
	return nil
}

func readyTranslations(ctx context.Context, tx *sql.Tx, id string) ([]Translation, error) {
	rows, err := tx.QueryContext(ctx, "SELECT language_code,title,slug,body_markdown FROM public.article_draft_translations WHERE article_id=$1 AND is_ready ORDER BY language_code COLLATE \"C\"", id)
	if err != nil {
		return nil, databaseError(err)
	}
	return scanTranslations(rows)
}

func currentTranslations(ctx context.Context, tx *sql.Tx, revision *string) ([]Translation, error) {
	if revision == nil {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT language_code,title,slug,body_markdown FROM public.article_revision_translations WHERE revision_id=$1 ORDER BY language_code COLLATE \"C\"", revision)
	if err != nil {
		return nil, databaseError(err)
	}
	return scanTranslations(rows)
}

func scanTranslations(rows *sql.Rows) ([]Translation, error) {
	defer func() { _ = rows.Close() }()
	result := make([]Translation, 0)
	for rows.Next() {
		var value Translation
		if err := rows.Scan(&value.LanguageCode, &value.Title, &value.Slug, &value.BodyMarkdown); err != nil {
			return nil, databaseError(err)
		}
		result = append(result, value)
	}
	return result, databaseError(rows.Err())
}

func lockLanguages(ctx context.Context, tx *sql.Tx, codes []string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT code,is_enabled FROM public.lock_languages($1::text[])", codes)
	if err != nil {
		return nil, databaseError(err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]bool, len(codes))
	for rows.Next() {
		var code string
		var enabled bool
		if err := rows.Scan(&code, &enabled); err != nil {
			return nil, databaseError(err)
		}
		result[code] = enabled
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError(err)
	}
	if len(result) != len(codes) {
		return nil, problem(Validation, "unknown_language")
	}
	return result, nil
}

func publicationSnapshot(ctx context.Context, tx *sql.Tx, current, ready []Translation) ([]Translation, error) {
	if len(ready) == 0 {
		return nil, problem(Conflict, "no_draft")
	}
	previous := make(map[string]Translation, len(current))
	for _, translation := range current {
		previous[translation.LanguageCode] = translation
	}
	merged := make(map[string]Translation, len(current)+len(ready))
	for code, translation := range previous {
		merged[code] = translation
	}
	changed := false
	for _, translation := range ready {
		old, exists := previous[translation.LanguageCode]
		changed = changed || !exists || old != translation
		merged[translation.LanguageCode] = translation
	}
	codes := make([]string, 0, len(merged))
	for code := range merged {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	// Проверяем доступность по результату после ожидания SHARE, не по устаревшему чтению.
	enabled, err := lockLanguages(ctx, tx, codes)
	if err != nil {
		return nil, err
	}
	for _, translation := range ready {
		if !enabled[translation.LanguageCode] {
			return nil, problem(Validation, "language_unavailable")
		}
	}
	if !changed {
		return nil, problem(Conflict, "no_draft")
	}
	result := make([]Translation, 0, len(codes))
	for _, code := range codes {
		result = append(result, merged[code])
	}
	return result, nil
}

func reserveRoute(ctx context.Context, tx *sql.Tx, id string, translation Translation) error {
	// Даже no-op UPDATE запрещён guard: резервирование владельца никогда не меняется.
	_, err := tx.ExecContext(ctx, "INSERT INTO public.public_routes(language_code,slug,article_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", translation.LanguageCode, translation.Slug, id)
	if err != nil {
		return databaseError(err)
	}
	var owner string
	err = tx.QueryRowContext(ctx, "SELECT article_id FROM public.public_routes WHERE language_code=$1 AND slug=$2", translation.LanguageCode, translation.Slug).Scan(&owner)
	if err != nil {
		return databaseError(err)
	}
	if owner != id {
		return problem(Conflict, "slug_conflict")
	}
	return nil
}

func storeSnapshot(ctx context.Context, tx *sql.Tx, state ArticleState, revision string, translations, ready []Translation) error {
	for _, translation := range translations {
		if err := reserveRoute(ctx, tx, state.ID, translation); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO public.article_revisions(id,article_id,revision_no)
 SELECT $1,$2,COALESCE(max(revision_no),0)+1 FROM public.article_revisions WHERE article_id=$2`, revision, state.ID)
	if err != nil {
		return databaseError(err)
	}
	for _, translation := range translations {
		_, err = tx.ExecContext(ctx, "INSERT INTO public.article_revision_translations(article_id,revision_id,language_code,title,slug,body_markdown) VALUES($1,$2,$3,$4,$5,$6)", state.ID, revision, translation.LanguageCode, translation.Title, translation.Slug, translation.BodyMarkdown)
		if err != nil {
			return databaseError(err)
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE public.article_revisions SET sealed=true WHERE id=$1", revision); err != nil {
		return databaseError(err)
	}
	// Удаляем только выпущенные ready-строки; незавершённая работа других языков остаётся.
	codes := make([]string, 0, len(ready))
	for _, translation := range ready {
		codes = append(codes, translation.LanguageCode)
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM public.article_draft_translations WHERE article_id=$1 AND language_code=ANY($2::text[]) AND is_ready", state.ID, codes)
	return databaseError(err)
}
