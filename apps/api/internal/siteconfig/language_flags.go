package siteconfig

import (
	"context"
	"database/sql"

	"stepolonia/apps/api/internal/staticsvg"
)

const InvalidLanguageFlagCode = "invalid_language_flag"

const editableLanguageFlagsQuery = `SELECT l.code,COALESCE(f.svg,l.initial_flag_svg) AS svg
 FROM public.languages l
 LEFT JOIN public.site_revision_language_flags f ON f.revision_id=$1 AND f.language_code=l.code
 WHERE l.is_enabled AND COALESCE(f.svg,l.initial_flag_svg)<>''`

const publishedLanguageFlagsQuery = `SELECT language_code,svg FROM public.public_language_flags WHERE revision_id=$1`

func normalizeLanguageFlags(updates map[string]string) (map[string]string, error) {
	flags := make(map[string]string, len(updates))
	for code, source := range updates {
		normalized, err := normalize(code)
		if err != nil {
			return nil, failure(400, InvalidLanguageFlagCode)
		}
		if _, exists := flags[normalized]; exists {
			return nil, failure(400, InvalidLanguageFlagCode)
		}
		flag, err := staticsvg.Normalize(source)
		if err != nil {
			return nil, failure(400, InvalidLanguageFlagCode)
		}
		flags[normalized] = flag
	}
	return flags, nil
}

func readLanguageFlags(ctx context.Context, tx *sql.Tx, query string, revision any) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, query, revision)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	flags := make(map[string]string)
	for rows.Next() {
		var code, svg string
		if err = rows.Scan(&code, &svg); err != nil {
			return nil, dbError(err)
		}
		flags[code] = svg
	}
	return flags, dbError(rows.Err())
}

func storeLanguageFlags(ctx context.Context, tx *sql.Tx, revision string, updates map[string]string) error {
	// The previous editable revision remains current while its replacement is built.
	_, err := tx.ExecContext(ctx, `INSERT INTO public.site_revision_language_flags(revision_id,language_code,svg)
 SELECT $1,l.code,COALESCE(f.svg,l.initial_flag_svg)
 FROM public.languages l CROSS JOIN public.site_state s
 LEFT JOIN public.site_revision_language_flags f
 ON f.revision_id=COALESCE(s.draft_id,s.published_id) AND f.language_code=l.code
 WHERE s.singleton AND (f.language_code IS NOT NULL OR l.initial_flag_svg<>'')`, revision)
	if err != nil {
		return dbError(err)
	}
	for code, svg := range updates {
		if err = storeLanguageFlag(ctx, tx, revision, code, svg); err != nil {
			return err
		}
	}
	return nil
}

func storeLanguageFlag(ctx context.Context, tx *sql.Tx, revision, code, svg string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO public.site_revision_language_flags(revision_id,language_code,svg)
 SELECT $1,code,$3 FROM public.languages WHERE code=$2 AND is_enabled
 ON CONFLICT (revision_id,language_code) DO UPDATE SET svg=EXCLUDED.svg`, revision, code, svg)
	if err != nil {
		return dbError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return dbError(err)
	}
	if count == 0 {
		return failure(400, InvalidLanguageFlagCode)
	}
	return nil
}
