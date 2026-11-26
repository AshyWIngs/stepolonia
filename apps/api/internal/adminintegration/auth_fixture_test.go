package adminintegration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"stepolonia/apps/api/internal/adminhttp"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
	"stepolonia/apps/api/internal/testpostgres"
)

const previousTOTPCounterSQL = `UPDATE public.mfa_totp SET last_counter=last_counter-1 WHERE user_id=$1`

const currentDatabaseSQL = "SELECT current_database()"
const publisherTestRole = "sp_publisher"
const languageImporterTestRole = "sp_language_importer"
const nodeExecutable = "node"

type authFixtureState struct {
	db      *sql.DB
	service *identity.AuthService
	secret  string
	codes   []string
	user    string
	config  identity.AuthConfig
	runtime *sql.DB
}

func authFixture(t *testing.T) authFixtureState {
	t.Helper()
	db, bootstrap, config := bootstrapFixture(t)
	return authFixtureFromBootstrap(t, db, bootstrap, config)
}

func authFixtureFromBootstrap(t *testing.T, db *sql.DB, bootstrap *identity.BootstrapService, config identity.BootstrapConfig) authFixtureState {
	t.Helper()
	ctx := context.Background()
	enrollment, err := bootstrap.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	codes, err := bootstrap.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, bootstrapCode(t, db, enrollment.Secret))
	requireNoError(t, err)
	// Use a previous accepted step so login tests never wait for a wall clock tick.
	requireNoError(t, identity.WithWriteMutex(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, previousTOTPCounterSQL, enrollment.UserID)
		return err
	}))
	service, err := identity.NewAuthService(db, config)
	requireNoError(t, err)
	requireNoError(t, service.CheckReady(ctx))
	return authFixtureState{db: db, service: service, secret: enrollment.Secret, codes: codes, user: enrollment.UserID, config: config}
}
func authReq(tokens identity.SessionTokens) identity.AuthRequest {
	return identity.AuthRequest{Token: tokens.Token, CSRF: tokens.CSRF, ClientIP: netip.MustParseAddr("192.0.2.1")}
}

func authRefreshStep(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	_, err := db.Exec(`UPDATE public.sessions SET step_up_at=last_seen WHERE token_digest=$1`, authDigest(token))
	requireNoError(t, err)
}

func authLogin(t *testing.T, f authFixtureState) identity.SessionTokens {
	t.Helper()
	ctx := context.Background()
	pre, err := f.service.NewPreauth(ctx)
	requireNoError(t, err)
	login, err := f.service.Login(ctx, authReq(pre), bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	return login
}
func authFull(t *testing.T, f authFixtureState) identity.SessionTokens {
	t.Helper()
	full, err := f.service.VerifyMFA(context.Background(), authReq(authLogin(t, f)), bootstrapCode(t, f.db, f.secret))
	requireNoError(t, err)
	return full
}
func authDenied(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("unexpected authorization success")
	}
}
func authAllow(ctx context.Context, tx *sql.Tx, session identity.Session) error { return nil }

func authDigest(value string) []byte { d := sha256.Sum256([]byte(value)); return d[:] }

type authHTTPFixture struct {
	state  authFixtureState
	server *httptest.Server
	client *http.Client
}

func newAuthHTTPFixture(t *testing.T) authHTTPFixture {
	t.Helper()
	return newAuthHTTPFixtureWithHandler(t, func(_ authFixtureState, auth http.Handler) http.Handler {
		return platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	})
}

func newAuthHTTPFixtureWithHandler(t *testing.T, wrap func(authFixtureState, http.Handler) http.Handler) authHTTPFixture {
	t.Helper()
	return newAuthHTTPFixtureWithEditors(t, wrap, false)
}

func newAuthHTTPFixtureWithEditors(t *testing.T, wrap func(authFixtureState, http.Handler) http.Handler, editorsEnabled bool) authHTTPFixture {
	t.Helper()
	return newAuthHTTPFixtureWithPublication(t, wrap, editorsEnabled, false)
}

func newAuthHTTPFixtureWithPublication(t *testing.T, wrap func(authFixtureState, http.Handler) http.Handler, editorsEnabled, siteEnabled bool) authHTTPFixture {
	t.Helper()
	owner, bootstrap, config := bootstrapFixture(t)
	if editorsEnabled {
		requireNoError(t, content.SeedInitialRegistry(context.Background(), owner))
	}
	f := authFixtureFromBootstrap(t, owner, bootstrap, config)
	cfg := testpostgres.AdminConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	role := cfg.Database + "_auth"
	quoted := pgx.Identifier{role}.Sanitize()
	_, err := f.db.Exec("CREATE ROLE " + quoted + " LOGIN IN ROLE sp_security")
	requireNoError(t, err)
	t.Cleanup(func() { _, _ = f.db.Exec("DROP OWNED BY " + quoted); _, _ = f.db.Exec("DROP ROLE " + quoted) })
	_, err = f.db.Exec("REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " FROM PUBLIC")
	requireNoError(t, err)
	cfg.User = role
	db := database.Open(cfg)
	t.Cleanup(func() { _ = db.Close() })
	requireNoError(t, database.CheckSecurity(context.Background(), db))
	service, err := identity.NewAuthService(db, f.config)
	requireNoError(t, err)
	requireNoError(t, service.CheckReady(context.Background()))
	var settings *identity.AuthService
	var editor adminhttp.DraftEditor
	if editorsEnabled {
		settingsDB := openDraftEditorLogin(t, f.db)
		editor, err = database.NewDraftEditor(context.Background(), settingsDB, f.config)
		requireNoError(t, err)
		settings, err = identity.NewAuthService(settingsDB, f.config)
		requireNoError(t, err)
	}
	var site *siteconfig.Reader
	if siteEnabled {
		f.runtime = openPublicationRuntimeLogin(t, f.db)
		site = siteconfig.NewReader(f.runtime)
	}
	server := newAuthTLSServerWithEditors(t, service, editor, settings, site, func(auth http.Handler) http.Handler { return wrap(f, auth) })
	client := server.Client()
	client.Jar, err = cookiejar.New(nil)
	requireNoError(t, err)
	return authHTTPFixture{f, server, client}
}

func newAuthTLSServer(t *testing.T, service *identity.AuthService, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	return newAuthTLSServerWithEditors(t, service, nil, nil, nil, wrap)
}

func newAuthTLSServerWithEditors(t *testing.T, service *identity.AuthService, editor adminhttp.DraftEditor, settings *identity.AuthService, site *siteconfig.Reader, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	auth, err := adminhttp.NewHandlerWithEditors(adminhttp.Config{CanonicalOrigin: origin, PublishedSite: site}, service, editor, settings)
	requireNoError(t, err)
	server.Config.Handler = wrap(auth)
	server.StartTLS()
	return server
}

func (f authHTTPFixture) request(t *testing.T, method, path, csrf, body string, status int) (map[string]string, *http.Response) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	requireNoError(t, err)
	setAuthHTTPHeaders(req, f.server.URL, csrf)
	res, err := f.client.Do(req)
	requireNoError(t, err)
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	requireNoError(t, err)
	if res.StatusCode != status {
		t.Fatalf("%s %s status=%d body=%s", method, path, res.StatusCode, data)
	}
	assertAuthNoStore(t, res)
	out := map[string]string{}
	if status == http.StatusOK {
		requireNoError(t, json.Unmarshal(data, &out))
	}
	return out, res
}

// Use the real restricted LOGIN for commands; retain the owner only for assertions.
func newRestrictedAuthHTTPFixture(t *testing.T) authHTTPFixture {
	t.Helper()
	f := newAuthHTTPFixture(t)
	f.state.service = newRestrictedAuthService(t, f.state)
	return f
}

func newRestrictedAuthService(t *testing.T, f authFixtureState) *identity.AuthService {
	t.Helper()
	cfg := testpostgres.AdminConfig(t)
	requireNoError(t, f.db.QueryRow(currentDatabaseSQL).Scan(&cfg.Database))
	cfg.User = cfg.Database + "_auth"
	pool := database.Open(cfg)
	t.Cleanup(func() { _ = pool.Close() })
	requireNoError(t, database.CheckSecurity(context.Background(), pool))
	service, err := identity.NewAuthService(pool, f.config)
	requireNoError(t, err)
	requireNoError(t, service.CheckReady(context.Background()))
	return service
}

func expireSessionStepUp(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	_, err := db.Exec(`WITH snapshot AS (SELECT clock_timestamp() AS n) UPDATE public.sessions SET created_at=n-interval '10 minutes',last_seen=n,step_up_at=n-interval '6 minutes',expires_at=n+interval '11 hours' FROM snapshot WHERE token_digest=$1`, authDigest(token))
	requireNoError(t, err)
}

func snapshotUserSecurityState(t *testing.T, db *sql.DB, user string) string {
	t.Helper()
	var state string
	requireNoError(t, db.QueryRow(`SELECT jsonb_build_object(
 'user',(SELECT to_jsonb(u) FROM public.users u WHERE u.id=$1),
 'tokens',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.digest) FROM public.onboarding_tokens t WHERE t.user_id=$1),
 'attempts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.session_id) FROM public.enrollment_attempts a WHERE a.user_id=$1),
 'sessions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM public.sessions s WHERE s.user_id=$1),
 'mfa',(SELECT to_jsonb(m) FROM public.mfa_totp m WHERE m.user_id=$1),
 'codes',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.digest) FROM public.recovery_codes c WHERE c.user_id=$1))::text`, user).Scan(&state))
	return state
}

func createAssignedTestRole(t *testing.T, f authFixtureState, name, state, userID string) string {
	t.Helper()
	var roleID string
	requireNoError(t, f.db.QueryRow(`INSERT INTO public.roles(id,name,state) VALUES(gen_random_uuid(),$1,$2) RETURNING id::text`, name, state).Scan(&roleID))
	_, err := f.db.Exec(`INSERT INTO public.user_roles(user_id,role_id) VALUES($1,$2)`, userID, roleID)
	requireNoError(t, err)
	return roleID
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != want {
		t.Fatalf("expected SQLSTATE %s; got %v", want, err)
	}
}

func bootstrapAdminRoleID(t *testing.T, f authFixtureState) string {
	t.Helper()
	var roleID string
	requireNoError(t, f.db.QueryRow(`SELECT role_id::text FROM public.user_roles WHERE user_id=$1`, f.user).Scan(&roleID))
	return roleID
}
