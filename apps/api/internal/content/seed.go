package content

import (
	"context"
	"database/sql"
	"stepolonia/apps/api/internal/cms"
)

// SeedInitialRegistry is a fixed infrastructure seed, never an editorial command.
// It requires a direct database owner and cannot create arbitrary types or content.
func SeedInitialRegistry(ctx context.Context, db *sql.DB) error {
	return cms.WithWriteMutex(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		var owner bool
		if err := tx.QueryRowContext(ctx, `SELECT current_user=session_user AND current_user=pg_catalog.pg_get_userbyid(datdba) FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&owner); err != nil {
			return err
		}
		if !owner {
			return problem(Forbidden, string(Forbidden))
		}
		var exists, empty, completed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.content_types WHERE key='article'),NOT EXISTS(SELECT 1 FROM public.content_types),(SELECT bootstrap_completed FROM public.security_state WHERE id=1)`).Scan(&exists, &empty, &completed); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if !empty || completed {
			return problem(Conflict, "registry_seed_conflict")
		}
		_, err := createType(ctx, tx, initialType())
		return err
	})
}
func initialType() TypeInput {
	return TypeInput{Key: initialArticleKey, RouteNamespace: articlesNamespace, Fields: []Definition{{Key: titleFieldKey, Kind: kindString, Scope: scopeLocalized, Visibility: visibilityPublic, Required: true, Config: []byte(`{"min_length":1,"max_length":500}`)}, {Key: slugFieldKey, Kind: kindString, Scope: scopeLocalized, Visibility: visibilityPublic, Required: true, Config: []byte(`{"slug":true,"max_length":200}`)}, {Key: bodyFieldKey, Kind: kindMarkdown, Scope: scopeLocalized, Visibility: visibilityPublic, Required: true, Config: []byte(`{"min_length":1}`)}}}
}
