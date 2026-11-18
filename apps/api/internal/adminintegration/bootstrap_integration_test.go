package adminintegration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/testpostgres"
	"strings"
	"sync"
	"testing"
	"time"
)

const bootstrapTestPassphrase = "Local test enrollment ! 73029"
const bootstrapTestLogin = "operator@example.test"
const bootstrapTestSite = "example.test"

const bootstrapOpCreate = "content.items.create"
const bootstrapOpRead = "content.items.read"
const bootstrapOpUpdate = "content.items.update"
const bootstrapOpReady = "content.items.ready"
const bootstrapOpPublish = "content.items.publish"
const bootstrapAssigned = "assigned"
const bootstrapAny = "any"

func bootstrapFixture(t *testing.T) (*sql.DB, *identity.BootstrapService, identity.BootstrapConfig) {
	t.Helper()
	cfg := testpostgres.AdminConfig(t)
	admin := database.Open(cfg)
	t.Cleanup(func() { _ = admin.Close() })
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "stage31_bootstrap_" + hex.EncodeToString(suffix)
	_, err := admin.Exec("CREATE DATABASE " + pgx.Identifier{name}.Sanitize())
	requireNoError(t, err)
	local := cfg.Copy()
	local.Database = name
	db := database.Open(local)
	db.SetMaxOpenConns(6)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)")
	})
	ctx := context.Background()
	requireNoError(t, database.PrepareRoles(ctx, db))
	provider, err := database.Provider(db)
	requireNoError(t, err)
	_, err = provider.Up(ctx)
	requireNoError(t, err)
	key := make([]byte, 32)
	_, err = rand.Read(key)
	requireNoError(t, err)
	conf := identity.BootstrapConfig{Site: bootstrapTestSite, EncryptionKey: key, KeyVersion: 1}
	svc, err := identity.NewBootstrapService(db, conf)
	requireNoError(t, err)
	return db, svc, conf
}

func bootstrapCode(t *testing.T, db *sql.DB, secret string) string {
	t.Helper()
	var now time.Time
	requireNoError(t, db.QueryRow("SELECT clock_timestamp()").Scan(&now))
	code, err := totp.GenerateCode(secret, now)
	requireNoError(t, err)
	return code
}

func assertBootstrapPending(t *testing.T, db *sql.DB) {
	t.Helper()
	var okay bool
	requireNoError(t, db.QueryRow(`SELECT NOT bootstrap_completed AND
 (SELECT count(*) FROM public.users WHERE state='pending')=1 AND
 (SELECT count(*) FROM public.users WHERE state='active')=0 AND
 (SELECT count(*) FROM public.user_roles)=0 AND
 (SELECT count(*) FROM public.recovery_codes)=0 FROM public.security_state`).Scan(&okay))
	if !okay {
		t.Fatal("partial enrollment acquired access or recovery codes")
	}
}

func TestBootstrapSuccessAndReplayIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	enrollment, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	assertBootstrapPending(t, db)
	code := bootstrapCode(t, db, enrollment.Secret)
	codes, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code)
	requireNoError(t, err)
	if len(codes) != 10 {
		t.Fatal("not ten recovery codes")
	}
	var complete bool
	requireNoError(t, db.QueryRow(`SELECT bootstrap_completed AND (SELECT state='active' FROM public.users WHERE id=bootstrap_user_id) AND (SELECT confirmed_at IS NOT NULL AND last_counter>=0 FROM public.mfa_totp WHERE user_id=bootstrap_user_id) FROM public.security_state`).Scan(&complete))
	if !complete {
		t.Fatal("activation or marker missing")
	}
	assertBootstrapRecoveryDigests(t, db, codes)

	if _, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code); err == nil {
		t.Fatal("completed TOTP reused")
	}
	if _, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase); err == nil {
		t.Fatal("bootstrap marker replayed")
	}
	_, err = db.Exec(`UPDATE public.users SET state='disabled'`)
	requireNoError(t, err)
	if _, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase); err == nil {
		t.Fatal("zero active admins reopened bootstrap")
	}
	for _, sql := range []string{`UPDATE public.security_state SET bootstrap_completed=false`, `DELETE FROM public.security_state`, `TRUNCATE public.security_state`, `UPDATE public.security_audit SET result='denied'`, `DELETE FROM public.security_audit`, `TRUNCATE public.security_audit`} {
		if _, err := db.Exec(sql); err == nil {
			t.Fatal("immutable security state or audit changed")
		}
	}
	var audit string
	requireNoError(t, db.QueryRow("SELECT string_agg(row_to_json(a)::text,' ') FROM public.security_audit a").Scan(&audit))
	for _, secret := range append(codes, bootstrapTestPassphrase, enrollment.Secret, "$argon2id$", code) {
		if strings.Contains(audit, secret) {
			t.Fatal("secret present in audit")
		}
	}
}

func assertBootstrapRecoveryDigests(t *testing.T, db *sql.DB, codes []string) {
	t.Helper()
	for _, code := range codes {
		raw, err := hex.DecodeString(code)
		if err != nil || len(raw) < 16 {
			t.Fatal("insufficient recovery entropy")
		}
		digest := sha256.Sum256([]byte(code))
		var count int
		requireNoError(t, db.QueryRow("SELECT count(*) FROM public.recovery_codes WHERE digest=$1 AND consumed_at IS NULL", digest[:]).Scan(&count))
		if count != 1 {
			t.Fatal("recovery digest missing")
		}
	}
}

func TestBootstrapInterruptionAndKeyLossIntegration(t *testing.T) {
	db, svc, conf := bootstrapFixture(t)
	ctx := context.Background()
	old, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	if _, err := svc.Start(ctx, bootstrapTestLogin, "Wrong enrollment password 947"); err == nil {
		t.Fatal("pending enrollment replaced without password")
	}
	bad := conf
	bad.EncryptionKey = make([]byte, 32)
	wrong, err := identity.NewBootstrapService(db, bad)
	requireNoError(t, err)
	if _, err := wrong.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase); err == nil {
		t.Fatal("lost key silently rotated")
	}
	bad = conf
	bad.Site = "other.test"
	other, err := identity.NewBootstrapService(db, bad)
	requireNoError(t, err)
	if _, err := other.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase); err == nil {
		t.Fatal("other installation resumed")
	}
	fresh, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	if fresh.Secret == old.Secret {
		t.Fatal("old seed reissued")
	}
	if _, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, "invalid"); err == nil {
		t.Fatal("invalid TOTP accepted")
	}
	assertBootstrapPending(t, db)
	_, err = svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, bootstrapCode(t, db, fresh.Secret))
	requireNoError(t, err)
}

func TestBootstrapConcurrentCompletionIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	en, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	code := bootstrapCode(t, db, en.Secret)
	start := make(chan struct{})
	out := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code)
			out <- err
		}()
	}
	close(start)
	success := 0
	for range 2 {
		if <-out == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent completion commits=%d", success)
	}
	var n int
	requireNoError(t, db.QueryRow("SELECT count(*) FROM public.recovery_codes").Scan(&n))
	if n != 10 {
		t.Fatal("concurrent completion changed code set")
	}
}

func TestBootstrapConcurrentStartIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	out := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Start(ctx, fmt.Sprintf("operator%d@example.test", i), bootstrapTestPassphrase)
			out <- err
		}(i)
	}
	close(start)
	wg.Wait()
	success := 0
	for range 2 {
		if <-out == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent initial bootstrap successes=%d", success)
	}
	assertBootstrapPending(t, db)
}

func TestBootstrapAuditFailureRollbackIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	_, err := db.Exec(`CREATE FUNCTION public.reject_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER reject_test_audit BEFORE INSERT ON public.security_audit FOR EACH STATEMENT EXECUTE FUNCTION public.reject_test_audit()`)
	requireNoError(t, err)
	if _, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase); err == nil {
		t.Fatal("start bypassed failed audit")
	}
	var n int
	requireNoError(t, db.QueryRow("SELECT count(*) FROM public.users").Scan(&n))
	if n != 0 {
		t.Fatal("failed start audit left account")
	}
	_, err = db.Exec(`DROP TRIGGER reject_test_audit ON public.security_audit`)
	requireNoError(t, err)
	en, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER reject_test_audit BEFORE INSERT ON public.security_audit FOR EACH STATEMENT EXECUTE FUNCTION public.reject_test_audit()`)
	requireNoError(t, err)
	code := bootstrapCode(t, db, en.Secret)
	if codes, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code); err == nil || len(codes) != 0 {
		t.Fatal("failed completion audit leaked codes or committed")
	}
	assertBootstrapPending(t, db)
	_, err = db.Exec(`DROP TRIGGER reject_test_audit ON public.security_audit`)
	requireNoError(t, err)
	_, err = svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code)
	requireNoError(t, err)
}

func TestBootstrapExactSeedGrantsIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	requireNoError(t, content.SeedInitialRegistry(ctx, db))
	var article string
	requireNoError(t, db.QueryRow("SELECT id::text FROM public.content_types WHERE key='article'").Scan(&article))
	en, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	_, err = svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, bootstrapCode(t, db, en.Secret))
	requireNoError(t, err)
	rows, err := db.Query(`SELECT r.name,g.operation,g.scope,COALESCE(g.type_id::text,''),COALESCE(g.object_scope,''),g.can_delegate,(SELECT count(*) FROM public.grant_fields f WHERE f.grant_id=g.id) FROM public.roles r JOIN public.role_grants g ON r.id=g.role_id ORDER BY r.name,g.operation`)
	requireNoError(t, err)
	defer func() { _ = rows.Close() }()
	expected := expectedBootstrapGrants()
	count := 0
	for rows.Next() {
		assertBootstrapGrantRow(t, rows, expected, article)
		count++
	}
	requireNoError(t, rows.Err())
	if count != 35 {
		t.Fatalf("seed grant count=%d", count)
	}
	for role, ops := range expected {
		if len(ops) > 0 {
			t.Fatalf("missing seed grants %s", role)
		}
	}
	var authority bool
	requireNoError(t, db.QueryRow(`SELECT target_scope='installation' AND object_ceiling='any' AND may_delegate_content AND can_delegate AND cardinality(delegate_operations)=7 AND delegate_operations @> ARRAY['content.items.create','content.items.read','content.items.update','content.items.ready','content.items.publish','content.items.unpublish','content.items.assign']::text[] FROM public.role_grants WHERE operation='content.permissions.delegate'`).Scan(&authority))
	if !authority {
		t.Fatal("delegation authority incomplete")
	}
	_, err = db.Exec(`INSERT INTO public.content_fields(type_id,field_id,key) VALUES($1,'b0000000-0000-0000-0000-000000000006','future')`, article)
	requireNoError(t, err)
	var leaked int
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM public.grant_fields WHERE field_id='b0000000-0000-0000-0000-000000000006'`).Scan(&leaked))
	if leaked != 0 {
		t.Fatal("new field automatically granted")
	}
}

func assertBootstrapGrantRow(t *testing.T, rows *sql.Rows, expected map[string]map[string]string, article string) {
	t.Helper()
	var role, op, scope, typeID, object string
	var delegate bool
	var fields int
	requireNoError(t, rows.Scan(&role, &op, &scope, &typeID, &object, &delegate, &fields))
	want, ok := expected[role][op]
	if !ok || want != object {
		t.Fatalf("unexpected seed grant %s/%s/%s", role, op, object)
	}
	delete(expected[role], op)

	if delegate != (role == "Site Admin") {
		t.Fatal("seed delegation mismatch")
	}
	if strings.HasPrefix(op, "content.items.") {
		if scope != "content" || typeID != article {
			t.Fatal("seed content granted outside Article")
		}
		wantFields := 0
		if bootstrapFieldOperation(op) {
			wantFields = 3
		}
		if fields != wantFields {
			t.Fatal("seed field set differs from existing exact fields")
		}
	} else if scope != "installation" || typeID != "" || fields != 0 {
		t.Fatal("installation authority grants data access")
	}
}

func expectedBootstrapGrants() map[string]map[string]string {
	expected := map[string]map[string]string{
		"Author":     {bootstrapOpCreate: "type", bootstrapOpRead: bootstrapAssigned, bootstrapOpUpdate: bootstrapAssigned, bootstrapOpReady: bootstrapAssigned},
		"Editor":     {bootstrapOpRead: bootstrapAny, bootstrapOpUpdate: bootstrapAny, bootstrapOpReady: bootstrapAny, bootstrapOpPublish: bootstrapAny, "content.items.unpublish": bootstrapAny},
		"Site Admin": {bootstrapOpCreate: "type", bootstrapOpRead: bootstrapAny, bootstrapOpUpdate: bootstrapAny, bootstrapOpReady: bootstrapAny, bootstrapOpPublish: bootstrapAny, "content.items.unpublish": bootstrapAny, "content.items.assign": bootstrapAny},
	}
	installation := []string{"content_types.manage", "content_types.initialize_access", "content.permissions.delegate", "users.create", "users.read", "users.activate", "users.disable", "roles.create", "roles.read", "roles.edit", "roles.assign", "roles.disable", "roles.enable", "roles.archive", "roles.restore", "roles.delete", "sessions.revoke_any", "audit.read", "settings.manage"}
	for _, op := range installation {
		expected["Site Admin"][op] = ""
	}
	return expected
}

func TestBootstrapSQLIsolationIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	en, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	_, err = svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, bootstrapCode(t, db, en.Secret))
	requireNoError(t, err)
	tables := []string{"users", "roles", "user_roles", "role_grants", "grant_fields", "mfa_totp", "recovery_codes", "security_state", "security_audit"}
	for _, role := range []string{publisherTestRole, languageImporterTestRole} {
		for _, table := range tables {
			tx, err := db.BeginTx(ctx, nil)
			requireNoError(t, err)
			_, err = tx.ExecContext(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize())
			requireNoError(t, err)
			_, err = tx.ExecContext(ctx, "SELECT * FROM "+pgx.Identifier{"public", table}.Sanitize())
			_ = tx.Rollback()
			if err == nil {
				t.Fatalf("%s can read %s", role, table)
			}
		}
	}
	role := "sp_bootstrap_runtime_" + strings.ReplaceAll(en.UserID, "-", "")
	_, err = db.Exec("CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " NOLOGIN")
	requireNoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec("DROP OWNED BY " + pgx.Identifier{role}.Sanitize())
		_, _ = db.Exec("DROP ROLE " + pgx.Identifier{role}.Sanitize())
	})
	for _, table := range tables {
		var access bool
		requireNoError(t, db.QueryRow("SELECT has_table_privilege($1,$2,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')", role, "public."+table).Scan(&access))
		if access {
			t.Fatalf("runtime security access %s", table)
		}
	}
	conf := identity.BootstrapConfig{Site: bootstrapTestSite, EncryptionKey: make([]byte, 32), KeyVersion: 1}
	cfg := testpostgres.AdminConfig(t)
	cfg.Database = func() string {
		var n string
		requireNoError(t, db.QueryRow("SELECT current_database()").Scan(&n))
		return n
	}()
	cfg.RuntimeParams["role"] = publisherTestRole
	publisher := database.Open(cfg)
	defer func() { _ = publisher.Close() }()
	restricted, err := identity.NewBootstrapService(publisher, conf)
	requireNoError(t, err)
	if _, err := restricted.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase); err == nil {
		t.Fatal("publisher used operator exception")
	}
}

func bootstrapFieldOperation(op string) bool {
	switch op {
	case bootstrapOpCreate, bootstrapOpRead, bootstrapOpUpdate, bootstrapOpReady, bootstrapOpPublish:
		return true
	}
	return false
}

func TestBootstrapDeniedEnrollmentAuditIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	if _, err := svc.Start(ctx, bootstrapTestLogin, "too short"); err == nil {
		t.Fatal("weak password accepted")
	}
	var n int
	requireNoError(t, db.QueryRow("SELECT count(*) FROM public.security_audit").Scan(&n))
	if n != 0 {
		t.Fatal("invalid first request poisoned empty security state")
	}
	en, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	if _, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, "badcode"); err == nil {
		t.Fatal("invalid TOTP accepted")
	}
	assertBootstrapPending(t, db)
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM public.security_audit WHERE operation='bootstrap.denied' AND result='denied' AND target_id=$1`, en.UserID).Scan(&n))
	if n != 1 {
		t.Fatal("failed enrollment denial audit missing")
	}
	if _, err := svc.Start(ctx, bootstrapTestLogin, "Wrong password for enrollment 71"); err == nil {
		t.Fatal("wrong restart credential accepted")
	}
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM public.security_audit WHERE operation='bootstrap.denied' AND result='denied'`).Scan(&n))
	if n != 2 {
		t.Fatal("failed password denial audit missing")
	}
	_, err = db.Exec(`CREATE FUNCTION public.reject_denial_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER reject_denial_audit BEFORE INSERT ON public.security_audit FOR EACH STATEMENT EXECUTE FUNCTION public.reject_denial_audit()`)
	requireNoError(t, err)
	if codes, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, "badcode"); err == nil || len(codes) != 0 {
		t.Fatal("failed denied audit allowed enrollment")
	}
	assertBootstrapPending(t, db)
}
