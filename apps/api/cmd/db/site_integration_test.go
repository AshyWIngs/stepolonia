package main

import (
	"context"
	"database/sql"
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

	var refused inputError
	if err := initializeSite(ctx, db, "Wrong locale portal", "fr"); !errors.As(err, &refused) {
		t.Fatalf("expected a refusal for a missing initial catalog, got %v", err)
	}
	assertPristineSite(t, ctx, db)
	if _, err := db.ExecContext(ctx, `UPDATE public.languages SET name='Polish (edited)' WHERE code='pl';
 UPDATE public.initial_site_defaults SET accent='#324abc' WHERE singleton`); err != nil {
		t.Fatal(err)
	}

	const portalName = "Polacy w Kazachstanie"
	if err := initializeSite(ctx, db, portalName, "pl"); err != nil {
		t.Fatal(err)
	}
	if err := initializeSite(ctx, db, "Another portal", "en"); !errors.As(err, &refused) {
		t.Fatalf("expected a refusal to replace initialized settings, got %v", err)
	}
	assertInitializedSite(t, ctx, db, portalName)
}

func assertPristineSite(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var pristine bool
	if err := db.QueryRowContext(ctx, `SELECT version=1 AND draft_id IS NULL AND published_id IS NULL
 AND NOT EXISTS (SELECT 1 FROM public.languages WHERE is_enabled)
 FROM public.site_state WHERE singleton`).Scan(&pristine); err != nil {
		t.Fatal(err)
	}
	if !pristine {
		t.Fatal("invalid initialization changed site state or languages")
	}
}

func assertInitializedSite(t *testing.T, ctx context.Context, db *sql.DB, portalName string) {
	t.Helper()
	var title, accent, polishName string
	var locales, audits int
	err := db.QueryRowContext(ctx, `SELECT p.title, p.accent,
 (SELECT name FROM public.public_ui_languages WHERE language_code='pl'),
 (SELECT count(*) FROM public.public_ui_languages),
 (SELECT count(*) FROM public.security_audit WHERE operation='settings.initialized' AND target_id=p.id)
 FROM public.public_site p`).Scan(&title, &accent, &polishName, &locales, &audits)
	if err != nil {
		t.Fatal(err)
	}
	if title != portalName || accent != "#324abc" || polishName != "Polish (edited)" || locales != 4 || audits != 1 {
		t.Fatalf("unexpected publication: title=%q accent=%q language=%q catalogs=%d audit=%d", title, accent, polishName, locales, audits)
	}
	for code, want := range map[string]string{
		"en": "Portal details", "pl": "Informacje o portalu", "ru": "О портале", "kk": "Портал туралы",
	} {
		var value string
		err := db.QueryRowContext(ctx, `SELECT message_value FROM public.public_ui_messages
 WHERE language_code=$1 AND message_key='admin.settings_identity'`, code).Scan(&value)
		if err != nil || value != want {
			t.Fatalf("published %s catalog message %q, want %q: %v", code, value, want, err)
		}
	}
}
