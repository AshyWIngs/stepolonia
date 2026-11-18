package main

import (
	"context"
	"errors"
	"testing"

	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/testpostgres"
)

func TestAdminInitializeSiteIntegration(t *testing.T) {
	cfg := testpostgres.AdminConfig(t)
	db := database.Open(cfg)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := database.PrepareRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	provider, err := database.Provider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.Seed(ctx, db); err != nil {
		t.Fatal(err)
	}

	const portalName = "Polacy w Kazachstanie"
	if err := initializeSite(ctx, db, portalName, "pl"); err != nil {
		t.Fatal(err)
	}
	var refused inputError
	if err := initializeSite(ctx, db, "Another portal", "en"); !errors.As(err, &refused) {
		t.Fatalf("expected a refusal to replace initialized settings, got %v", err)
	}
	var title string
	var locales, audits int
	err = db.QueryRowContext(ctx, `SELECT p.title,
 (SELECT count(*) FROM public.public_ui_languages),
 (SELECT count(*) FROM public.security_audit WHERE operation='settings.initialized' AND target_id=p.id)
 FROM public.public_site p`).Scan(&title, &locales, &audits)
	if err != nil {
		t.Fatal(err)
	}
	if title != portalName || locales != 4 || audits != 1 {
		t.Fatalf("unexpected first publication: title=%q catalogs=%d audit=%d", title, locales, audits)
	}
}
