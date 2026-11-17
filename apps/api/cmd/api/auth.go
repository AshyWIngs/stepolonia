package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/cmshttp"
	"stepolonia/apps/api/internal/config"
	"stepolonia/apps/api/internal/database"
)

func authentication(ctx context.Context, runtime *pgxpool.Config) (http.Handler, func(), error) {
	cfg, err := config.Authentication(runtime)
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil {
		return nil, func() {}, nil
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
	service, err := cms.NewAuthService(db, cfg.Service)
	if err != nil {
		closePool()
		return nil, nil, err
	}
	if err = service.CheckReady(ctx); err != nil {
		closePool()
		return nil, nil, err
	}
	handler, err := cmshttp.NewHandler(cmshttp.Config{CanonicalOrigin: cfg.Origin, TrustedProxyCIDRs: cfg.TrustedProxyCIDRs}, service)
	if err != nil {
		closePool()
		return nil, nil, err
	}
	return handler, closePool, nil
}
