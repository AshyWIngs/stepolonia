package main

import (
	"context"
	"database/sql"
	"slices"

	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const commandInitializeSite = "initialize-site"

func initializeSite(ctx context.Context, db *sql.DB, title, language string) error {
	draft := apiexamples.DemoSite()
	if _, ok := draft.Catalogs[language]; !ok {
		return inputError{"initial language must have a bundled interface catalog"}
	}
	draft.Settings = siteconfig.Settings{
		Title: title, Accent: "#2563eb", DefaultLanguage: language, FallbackLanguage: language,
	}
	languages := make([]string, 0, len(draft.Catalogs))
	for code := range draft.Catalogs {
		languages = append(languages, code)
	}
	slices.Sort(languages)
	return identity.WithWriteMutex(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := checkInitialSite(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.languages SET is_enabled=true WHERE code=ANY($1::text[]) AND NOT is_enabled`, languages); err != nil {
			return err
		}
		version, err := siteconfig.SaveDraftTx(ctx, tx, 1, draft)
		if err != nil {
			return err
		}
		id, _, err := siteconfig.PublishTx(ctx, tx, version)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.security_audit(id,operation,target_id) VALUES(gen_random_uuid(),'settings.initialized',$1)`, id)
		return err
	})
}

func checkInitialSite(ctx context.Context, tx *sql.Tx) error {
	var owner bool
	if err := tx.QueryRowContext(ctx, `SELECT current_user=session_user AND datdba=current_user::regrole
 FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&owner); err != nil {
		return err
	}
	if !owner {
		return inputError{"initial publication requires a direct database-owner login"}
	}
	var pristine bool
	if err := tx.QueryRowContext(ctx, `SELECT version=1 AND draft_id IS NULL AND published_id IS NULL
 FROM public.site_state WHERE singleton FOR UPDATE`).Scan(&pristine); err != nil {
		return err
	}
	if !pristine {
		return inputError{"settings already exist; use Administration to edit their next revision"}
	}
	return nil
}
