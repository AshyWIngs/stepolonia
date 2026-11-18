package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// The golden catalog supplies the complete namespace inventory before Goose
// creates its history table or commits an earlier domain.
func checkFreshSchema(ctx context.Context, db *sql.DB) error {
	contract, err := readSchemaContract()
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for key := range contract.Columns {
		names[strings.SplitN(key, ".", 2)[0]] = true
	}
	for key := range contract.Views {
		names[key] = true
	}
	for key := range contract.Indexes {
		names[strings.SplitN(key, ".", 2)[1]] = true
	}
	// Retired application objects must not survive a fresh install.
	for _, name := range []string{"articles", "article_draft_translations", "article_revisions", "article_revision_translations", "public_routes", "public_article_routes", "public_article_translations", "auth_attempts", "auth_attempts_pkey", "roles_name_key_key"} {
		names[name] = true
	}
	relations := make([]string, 0, len(names))
	for name := range names {
		relations = append(relations, name)
	}
	functions := make([]string, 0, len(contract.Functions))
	for name := range contract.Functions {
		functions = append(functions, name)
	}
	var conflict bool
	err = db.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname=ANY($1::text[])) OR
 EXISTS(SELECT 1 FROM pg_catalog.pg_type WHERE typnamespace='public'::regnamespace AND typname=ANY($1::text[])) OR
 EXISTS(SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname=ANY($2::text[]))`, relations, functions).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return fmt.Errorf("application namespace conflict before migration DDL")
	}
	return nil
}
