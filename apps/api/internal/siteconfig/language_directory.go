package siteconfig

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	InvalidLanguageEntryCode   = "invalid_language_entry"
	LanguageLimitCode          = "language_limit"
	LanguageUsedInSettingsCode = "language_used_in_settings"
	LanguageUsedInContentCode  = "language_used_in_content"
	MaxLanguageNameLength      = 200
)

type LanguageEntry struct {
	Language         `json:"language"`
	Enabled          bool   `json:"enabled" doc:"Whether the language can be used in content and UI settings."`
	CatalogPublished bool   `json:"catalog_published" doc:"Whether an enabled language has a complete catalog in the active settings publication."`
	Version          string `json:"version" doc:"Use this version for edits and availability changes."`
}

const languageDirectoryQuery = `SELECT l.code,l.name,l.native_name,l.direction,l.is_enabled,l.generation,
 l.is_enabled AND EXISTS(SELECT 1 FROM public.site_state s JOIN public.site_revision_languages c
 ON c.revision_id=s.published_id WHERE s.singleton AND c.language_code=l.code)
 FROM public.languages l WHERE ($1='' OR l.code=$1) ORDER BY l.code`

func ReadLanguageDirectory(ctx context.Context, tx *sql.Tx, code string) ([]LanguageEntry, error) {
	if err := checkEditorRole(ctx, tx); err != nil {
		return nil, err
	}
	if code != "" {
		normalized, err := normalize(code)
		if err != nil {
			return nil, failure(400, InvalidLanguageEntryCode)
		}
		code = normalized
	}
	rows, err := tx.QueryContext(ctx, languageDirectoryQuery, code)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]LanguageEntry, 0)
	for rows.Next() {
		var item LanguageEntry
		var generation int64
		if err := rows.Scan(&item.Code, &item.Name, &item.NativeName, &item.Direction, &item.Enabled, &generation, &item.CatalogPublished); err != nil {
			return nil, dbError(err)
		}
		item.Version = settingsScopeVersion(struct {
			Language
			Generation int64
		}{item.Language, generation})
		result = append(result, item)
	}
	return result, dbError(rows.Err())
}

// NormalizeLanguageDefinition validates metadata shared by JSON and CSV imports.
func NormalizeLanguageDefinition(input Language) (Language, error) {
	code, err := normalize(strings.TrimSpace(input.Code))
	input.Name, input.NativeName = strings.TrimSpace(input.Name), strings.TrimSpace(input.NativeName)
	if err != nil || !validLanguageName(input.Name) || !validLanguageName(input.NativeName) ||
		(input.Direction != "ltr" && input.Direction != "rtl") {
		return Language{}, failure(400, InvalidLanguageEntryCode)
	}
	input.Code = code
	return input, nil
}

func SaveLanguageTx(ctx context.Context, tx *sql.Tx, expected string, input Language) (LanguageEntry, error) {
	input, err := NormalizeLanguageDefinition(input)
	if err != nil {
		return LanguageEntry{}, err
	}
	code := input.Code
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return LanguageEntry{}, err
	}
	current, err := matchLanguage(ctx, tx, code, expected)
	if err != nil {
		return LanguageEntry{}, err
	}
	if current.Code != "" && expected == "" {
		return LanguageEntry{}, failure(409, versionConflictCode)
	}
	if current.Code == "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO public.languages(code,name,native_name,direction) VALUES($1,$2,$3,$4)`,
			code, input.Name, input.NativeName, input.Direction)
	} else if current.Language != input {
		_, err = tx.ExecContext(ctx, `UPDATE public.languages SET name=$2,native_name=$3,direction=$4 WHERE code=$1`,
			code, input.Name, input.NativeName, input.Direction)
	}
	if err != nil {
		return LanguageEntry{}, dbError(err)
	}
	if current.CatalogPublished && current.Language != input {
		if err := republishLanguageMetadata(ctx, tx, state, code); err != nil {
			return LanguageEntry{}, err
		}
	}
	return matchLanguage(ctx, tx, code, "")
}

func republishLanguageMetadata(ctx context.Context, tx *sql.Tx, state publishedSettings, code string) error {
	messages, err := readScopedCatalog(ctx, tx, state.id, code)
	if err != nil {
		return err
	}
	id, err := createScopedRevisionExceptCatalog(ctx, tx, state, true, code)
	if err != nil {
		return err
	}
	if err := storeCatalog(ctx, tx, id, code, messages); err != nil {
		return err
	}
	_, err = activateScopedRevision(ctx, tx, state, id, "")
	return err
}

func SetLanguageAvailabilityTx(ctx context.Context, tx *sql.Tx, code, expected string, enabled bool) (LanguageEntry, error) {
	normalized, err := normalize(code)
	if err != nil || expected == "" {
		return LanguageEntry{}, failure(400, InvalidLanguageEntryCode)
	}
	if _, err := lockPublishedSettings(ctx, tx); err != nil {
		return LanguageEntry{}, err
	}
	current, err := matchLanguage(ctx, tx, normalized, expected)
	if err != nil || current.Enabled == enabled {
		return current, err
	}
	var conflict string
	if err := tx.QueryRowContext(ctx, `SELECT public.set_language_availability($1,$2)`, normalized, enabled).Scan(&conflict); err != nil {
		return LanguageEntry{}, dbError(err)
	}
	if conflict != "" {
		return LanguageEntry{}, failure(409, conflict)
	}
	return matchLanguage(ctx, tx, normalized, "")
}

func matchLanguage(ctx context.Context, tx *sql.Tx, code, expected string) (LanguageEntry, error) {
	entries, err := ReadLanguageDirectory(ctx, tx, code)
	if err != nil {
		return LanguageEntry{}, err
	}
	if len(entries) != 0 && (expected == "" || entries[0].Version == expected) {
		return entries[0], nil
	}
	if len(entries) == 0 && expected == "" {
		return LanguageEntry{}, nil
	}
	return LanguageEntry{}, failure(409, versionConflictCode)
}

func validLanguageName(value string) bool {
	return value != "" && utf8.RuneCountInString(value) <= MaxLanguageNameLength && strings.IndexFunc(value, unicode.IsControl) == -1
}
