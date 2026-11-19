package siteconfig

import (
	"context"
	"database/sql"
	"errors"
)

type siteQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ReadInitialDraft loads the owner's defaults for the first site publication.
func ReadInitialDraft(ctx context.Context, db siteQueryer) (Draft, error) {
	var draft Draft
	if err := db.QueryRowContext(ctx, `SELECT accent FROM public.initial_site_defaults WHERE singleton`).Scan(&draft.Settings.Accent); err != nil {
		return Draft{}, err
	}
	catalogs, err := readInitialCatalogs(ctx, db)
	if err != nil {
		return Draft{}, err
	}
	draft.Catalogs = catalogs
	draft.SlugRules, err = readSlugRules(ctx, db, initialSlugRulesQuery)
	if err != nil {
		return Draft{}, err
	}
	return draft, nil
}

func readInitialCatalogs(ctx context.Context, db siteQueryer) (map[string]map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT language_code, message_key, message_value FROM public.initial_ui_messages`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	catalogs := make(map[string]map[string]string)
	for rows.Next() {
		var language, key, value string
		if err := rows.Scan(&language, &key, &value); err != nil {
			return nil, err
		}
		if catalogs[language] == nil {
			catalogs[language] = make(map[string]string)
		}
		catalogs[language][key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(catalogs) == 0 {
		return nil, errors.New("initial UI catalogs are missing")
	}
	return catalogs, nil
}
