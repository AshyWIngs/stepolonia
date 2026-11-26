package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"stepolonia/apps/api/internal/adminhttp"
	"stepolonia/apps/api/internal/config"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

func authentication(ctx context.Context, runtime *pgxpool.Config, publishedSite *siteconfig.Reader) (http.Handler, func(), error) {
	cfg, err := config.Authentication(runtime)
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil {
		return nil, func() {
			// Disabled authentication created no private pool to close.
		}, nil
	}
	defer clear(cfg.Service.EncryptionKey)
	pool, err := pgxpool.NewWithConfig(ctx, cfg.Database)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize private authentication pool")
	}
	db := stdlib.OpenDBFromPool(pool)
	closePool := func() { _ = db.Close(); pool.Close() }
	if err = database.CheckSecurity(ctx, db); err != nil {
		closePool()
		return nil, nil, err
	}
	service, err := identity.NewAuthService(db, cfg.Service)
	if err != nil {
		closePool()
		return nil, nil, err
	}
	if err = service.CheckReady(ctx); err != nil {
		closePool()
		return nil, nil, err
	}
	editor, settings, closeDraft, err := openEditors(ctx, cfg)
	if err != nil {
		closePool()
		return nil, nil, err
	}
	closeAuth := func() { closeDraft(); closePool() }
	handler, err := adminhttp.NewHandlerWithEditors(adminhttp.Config{
		CanonicalOrigin: cfg.Origin, TrustedProxyCIDRs: cfg.TrustedProxyCIDRs, PublishedSite: publishedSite,
	}, service, editor, settings)
	if err != nil {
		closeAuth()
		return nil, nil, err
	}
	return handler, closeAuth, nil
}

func openEditors(ctx context.Context, cfg *config.AuthenticationConfig) (adminhttp.DraftEditor, *identity.AuthService, func(), error) {
	if cfg.DraftDatabase == nil {
		return nil, nil, func() {
			// A disabled editor has no database pool to close.
		}, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg.DraftDatabase)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize private draft editor pool")
	}
	db := stdlib.OpenDBFromPool(pool)
	closePool := func() { _ = db.Close(); pool.Close() }
	editor, err := database.NewDraftEditor(ctx, db, cfg.Service)
	if err != nil {
		closePool()
		return nil, nil, nil, err
	}
	settings, err := identity.NewAuthService(db, cfg.Service)
	if err != nil {
		closePool()
		return nil, nil, nil, err
	}
	return editor, settings, closePool, nil
}
