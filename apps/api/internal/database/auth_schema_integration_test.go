package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/cms"
)

const currentDatabaseSQL = "SELECT current_database()"

func TestAuthSchemaIntegration(t *testing.T) {
	db, runtime := domainQAFixture(t)
	ctx := context.Background()
	requireNoError(t, Up(ctx, db, runtime))
	for _, query := range []string{
		`INSERT INTO public.auth_attempts VALUES (CURRENT_TIMESTAMP,'account',decode(repeat('01',32),'hex'),6)`,
		`INSERT INTO public.auth_attempts VALUES (CURRENT_TIMESTAMP,'ip',decode(repeat('01',32),'hex'),31)`,
		`INSERT INTO public.auth_attempts VALUES (CURRENT_TIMESTAMP,'site',decode(repeat('01',32),'hex'),101)`,
		`INSERT INTO public.auth_attempts VALUES (CURRENT_TIMESTAMP,'other',decode(repeat('01',32),'hex'),1)`,
		`INSERT INTO public.security_audit(id,operation,reason) VALUES (gen_random_uuid(),'auth.denied','arbitrary secret')`,
	} {
		rejectSQL(t, ctx, db, "", query, "23514")
	}
	_, err := db.Exec(`INSERT INTO public.auth_attempts VALUES (CURRENT_TIMESTAMP,'account',decode(repeat('01',32),'hex'),5)`)
	requireNoError(t, err)

	testAuthRecoveryReservation(t, db)

	login := runtime + "_auth"
	quoted := pgx.Identifier{login}.Sanitize()
	_, err = db.Exec("CREATE ROLE " + quoted + " LOGIN IN ROLE sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec("DROP ROLE " + quoted) })
	cfg := cmsDisposableDatabaseConfig(t)
	requireNoError(t, db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = login
	security := Open(cfg)
	defer func() { _ = security.Close() }()
	requireNoError(t, CheckSecurity(ctx, security))
	testSecurityLoginIdentity(t, cfg)
	for _, query := range []string{
		`SELECT * FROM public.public_content_items`,
		`SELECT * FROM public.security_audit`,
		`DELETE FROM public.users`,
		`UPDATE public.security_audit SET reason=''`,
		`DELETE FROM public.recovery_codes`,
		`CREATE TABLE public.auth_escape(id integer)`,
		`CREATE TEMP TABLE auth_escape(id integer)`,
	} {
		_, err = security.Exec(query)
		assertSQLState(t, err, "42501")
	}

	_, err = db.Exec("GRANT DELETE ON public.users TO " + quoted)
	requireNoError(t, err)
	if err := CheckSecurity(ctx, security); err == nil {
		t.Fatal("authentication pool accepted excess direct security grants")
	}
	_, err = db.Exec("REVOKE DELETE ON public.users FROM " + quoted)
	requireNoError(t, err)

	_, err = db.Exec("GRANT sp_publisher TO " + quoted)
	requireNoError(t, err)
	if err := CheckSecurity(ctx, security); err == nil {
		t.Fatal("authentication pool accepted publisher membership")
	}
	_, err = db.Exec("REVOKE sp_publisher FROM " + quoted)
	requireNoError(t, err)
	requireNoError(t, CheckSecurity(ctx, security))
}

func testSecurityLoginIdentity(t *testing.T, safe *pgx.ConnConfig) {
	t.Helper()
	for _, option := range []string{"role", "session_authorization", "options"} {
		t.Run(option, func(t *testing.T) {
			owner := cmsDisposableDatabaseConfig(t)
			owner.Database = safe.Database
			value := safe.User
			if option == "options" {
				value = "-c session_authorization=" + safe.User
			}
			owner.RuntimeParams[option] = value
			masked := Open(owner)
			defer func() { _ = masked.Close() }()
			requireNoError(t, masked.PingContext(context.Background()))
			if err := CheckSecurity(context.Background(), masked); err == nil {
				t.Fatal("authentication pool accepted privileged authenticated login hidden by startup options")
			}
		})
	}
}

func testAuthRecoveryReservation(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO public.users(id,login_key) VALUES
 ('a2000000-0000-0000-0000-000000000001','auth-schema-user');
 INSERT INTO public.recovery_codes(user_id,digest) VALUES
 ('a2000000-0000-0000-0000-000000000001',decode(repeat('12',32),'hex'));
 INSERT INTO public.sessions(id,token_digest,csrf_digest,user_id,kind,credential_version,expires_at) VALUES
 ('a2000000-0000-0000-0000-000000000002',decode(repeat('13',32),'hex'),decode(repeat('14',32),'hex'),
 'a2000000-0000-0000-0000-000000000001','recovery',1,CURRENT_TIMESTAMP+interval '10 minutes'),
 ('a2000000-0000-0000-0000-000000000003',decode(repeat('15',32),'hex'),decode(repeat('16',32),'hex'),
 'a2000000-0000-0000-0000-000000000001','recovery',1,CURRENT_TIMESTAMP+interval '10 minutes')`)
	requireNoError(t, err)
	reservation := `INSERT INTO public.recovery_attempts
 (id,session_id,user_id,code_digest,credential_version,pending_ciphertext,key_version,expires_at)
 VALUES (gen_random_uuid(),$1,'a2000000-0000-0000-0000-000000000001',decode(repeat('12',32),'hex'),
 1,decode(repeat('17',48),'hex'),1,CURRENT_TIMESTAMP+interval '10 minutes')`
	_, err = db.Exec(reservation, "a2000000-0000-0000-0000-000000000002")
	requireNoError(t, err)
	_, err = db.Exec(reservation, "a2000000-0000-0000-0000-000000000003")
	assertSQLState(t, err, "23505")
	_, err = db.Exec(`UPDATE public.recovery_attempts SET ended_at=clock_timestamp()`)
	requireNoError(t, err)
	_, err = db.Exec(reservation, "a2000000-0000-0000-0000-000000000003")
	requireNoError(t, err)
	var consumed bool
	requireNoError(t, db.QueryRow(`SELECT consumed_at IS NOT NULL FROM public.recovery_codes`).Scan(&consumed))
	if consumed {
		t.Fatal("reservation consumed the recovery code")
	}

}

func TestAuthSecurityPoolIntegration(t *testing.T) {
	f := authFixture(t)
	ctx := context.Background()
	login := "authlogin_" + strings.ReplaceAll(f.user, "-", "")
	quoted := pgx.Identifier{login}.Sanitize()
	_, err := f.db.Exec("CREATE ROLE " + quoted + " LOGIN IN ROLE sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = f.db.Exec("DROP ROLE " + quoted) })
	cfg := cmsDisposableDatabaseConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	_, err = f.db.Exec("REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " FROM PUBLIC")
	requireNoError(t, err)
	cfg.User = login
	security := Open(cfg)
	defer func() { _ = security.Close() }()
	requireNoError(t, CheckSecurity(ctx, security))
	f.service, err = cms.NewAuthService(security, f.config)
	requireNoError(t, err)
	requireNoError(t, f.service.CheckReady(ctx))
	full := authFull(t, f)
	requireNoError(t, f.service.WithSession(ctx, authReq(full), true, authAllow))
	requireNoError(t, f.service.Logout(ctx, authReq(full), full.Kind))
	authDenied(t, f.service.WithSession(ctx, authReq(full), true, authAllow))
	_, err = f.db.Exec(`ALTER TABLE public.sessions DISABLE TRIGGER sessions_write_mutex`)
	requireNoError(t, err)
	if err := CheckSecurity(ctx, security); err == nil {
		t.Fatal("authentication pool accepted a disabled guard")
	}
	_, err = f.db.Exec(`ALTER TABLE public.sessions ENABLE TRIGGER sessions_write_mutex`)
	requireNoError(t, err)
	requireNoError(t, CheckSecurity(ctx, security))
}
