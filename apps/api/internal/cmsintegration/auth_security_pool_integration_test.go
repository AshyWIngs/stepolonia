package cmsintegration

import (
	"context"
	"stepolonia/apps/api/internal/testpostgres"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"stepolonia/apps/api/internal/cms"
	"stepolonia/apps/api/internal/database"
)

func TestAuthSecurityPoolIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	login := "authlogin_" + strings.ReplaceAll(f.user, "-", "")
	quoted := pgx.Identifier{login}.Sanitize()
	_, err := f.db.Exec("CREATE ROLE " + quoted + " LOGIN IN ROLE sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = f.db.Exec("DROP ROLE " + quoted) })
	cfg := testpostgres.CMSConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	_, err = f.db.Exec("REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " FROM PUBLIC")
	requireNoError(t, err)
	cfg.User = login
	security := database.Open(cfg)
	defer func() { _ = security.Close() }()
	requireNoError(t, database.CheckSecurity(ctx, security))
	f.service, err = cms.NewAuthService(security, f.config)
	requireNoError(t, err)
	requireNoError(t, f.service.CheckReady(ctx))
	full := authFull(t, f)
	requireNoError(t, f.service.WithSession(ctx, authReq(full), true, authAllow))
	requireNoError(t, f.service.Logout(ctx, authReq(full), full.Kind))
	authDenied(t, f.service.WithSession(ctx, authReq(full), true, authAllow))
	_, err = f.db.Exec(`ALTER TABLE public.sessions DISABLE TRIGGER sessions_write_mutex`)
	requireNoError(t, err)
	if err := database.CheckSecurity(ctx, security); err == nil {
		t.Fatal("authentication pool accepted a disabled guard")
	}
	_, err = f.db.Exec(`ALTER TABLE public.sessions ENABLE TRIGGER sessions_write_mutex`)
	requireNoError(t, err)
	requireNoError(t, database.CheckSecurity(ctx, security))
}
