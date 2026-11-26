package siteconfig

import (
	"context"
	"database/sql"
)

const (
	CatalogRegistryChangedCode = "catalog_registry_changed"
	InvalidCatalogChangeCode   = "invalid_catalog_change"
)

type CatalogChange struct {
	Language string
	Messages map[string]string
	Remove   bool
}

func CatalogVersion(messages map[string]string) string { return settingsScopeVersion(messages) }

func ApplyCatalogChangeTx(ctx context.Context, tx *sql.Tx, expected string, change CatalogChange) (ScopedPublication, error) {
	code, err := normalize(change.Language)
	if err != nil || code != change.Language || change.Remove == (len(change.Messages) > 0) {
		return ScopedPublication{}, failure(400, InvalidCatalogChangeCode)
	}
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return ScopedPublication{}, err
	}
	if change.Remove && code == state.settings.FallbackLanguage {
		return ScopedPublication{}, failure(400, InvalidCatalogChangeCode)
	}
	if err := requirePublishedCatalogKeys(ctx, tx, state.id); err != nil {
		return ScopedPublication{}, err
	}
	current, err := readScopedCatalog(ctx, tx, state.id, code)
	if err != nil {
		return ScopedPublication{}, err
	}
	if CatalogVersion(current) != expected {
		return ScopedPublication{}, failure(409, versionConflictCode)
	}
	next := mergeCatalogMessages(current, change)
	id, err := createScopedRevisionExceptCatalog(ctx, tx, state, true, code)
	if err != nil {
		return ScopedPublication{}, err
	}
	if !change.Remove {
		if err := storeCatalog(ctx, tx, id, code, next); err != nil {
			return ScopedPublication{}, err
		}
	}
	return activateScopedRevision(ctx, tx, state, id, CatalogVersion(next))
}

func requirePublishedCatalogKeys(ctx context.Context, tx *sql.Tx, revision string) error {
	var registryMatches bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(
 (SELECT message_key,placeholders FROM public.ui_message_keys
  EXCEPT SELECT message_key,placeholders FROM public.site_revision_keys WHERE revision_id=$1)
 UNION ALL
 (SELECT message_key,placeholders FROM public.site_revision_keys WHERE revision_id=$1
  EXCEPT SELECT message_key,placeholders FROM public.ui_message_keys))`, revision).Scan(&registryMatches)
	if err != nil {
		return dbError(err)
	}
	if !registryMatches {
		return failure(409, CatalogRegistryChangedCode)
	}
	return nil
}

func mergeCatalogMessages(current map[string]string, change CatalogChange) map[string]string {
	if change.Remove {
		return nil
	}
	if current == nil {
		current = make(map[string]string, len(change.Messages))
	}
	for key, value := range change.Messages {
		current[key] = value
	}
	return current
}

func readScopedCatalog(ctx context.Context, tx *sql.Tx, revision, code string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.message_key,m.message_value FROM public.site_revision_languages l
 JOIN public.site_revision_messages m ON m.revision_id=COALESCE(l.catalog_revision_id,l.revision_id)
 AND m.language_code=l.language_code
 WHERE l.revision_id=$1 AND l.language_code=$2`, revision, code)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	var values map[string]string
	for rows.Next() {
		if values == nil {
			values = make(map[string]string)
		}
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, dbError(err)
		}
		values[key] = value
	}
	return values, dbError(rows.Err())
}
