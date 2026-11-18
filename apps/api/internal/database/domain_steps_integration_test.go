package database

import (
	"context"
	"database/sql"
	"testing"
)

const securityTestRole = "sp_security"

func TestDomainMigrationsStepContractsIntegration(t *testing.T) {
	db, _ := domainQAFixture(t)
	ctx := context.Background()
	provider, err := Provider(db)
	requireNoError(t, err)
	_, err = provider.UpTo(ctx, 3)
	requireNoError(t, err)

	testDomainRegistryStep(t, ctx, db)

	_, err = provider.UpTo(ctx, 4)
	requireNoError(t, err)
	testDomainEngineStep(t, ctx, db)

	_, err = provider.UpTo(ctx, 5)
	requireNoError(t, err)
	testDomainIdentityStep(t, ctx, db)

	_, err = provider.Up(ctx)
	requireNoError(t, err)
	requireNoError(t, CheckSchema(ctx, db))
	testDomainAssigneeStep(t, ctx, db)
}

func testDomainRegistryStep(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	// Registry identity and sealing already work before engine tables exist.
	tx := beginRoleTx(t, ctx, db, publisherTestRole)
	execSQL(t, ctx, tx, `INSERT INTO public.content_types(id,key,route_namespace)
VALUES ('d1000000-0000-0000-0000-000000000001','domain_step','domain-step');
INSERT INTO public.content_schemas(type_id,schema_no)
VALUES ('d1000000-0000-0000-0000-000000000001',1);
INSERT INTO public.content_fields(type_id,field_id,key)
VALUES ('d1000000-0000-0000-0000-000000000001','d1000000-0000-0000-0000-000000000002','title');
INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility)
VALUES ('d1000000-0000-0000-0000-000000000001',1,'d1000000-0000-0000-0000-000000000002','string','shared','public');
UPDATE public.content_schemas SET sealed=true;
UPDATE public.content_types SET version=version+1;
UPDATE public.content_fields SET display_labels='{"en":{"label":"Title"}}'`)
	requireNoError(t, tx.Commit())
	for _, query := range []string{
		`UPDATE public.content_types SET key='changed'`,
		`DELETE FROM public.content_types`,
		`UPDATE public.content_fields SET key='changed'`,
		`DELETE FROM public.content_fields`,
		`UPDATE public.content_schemas SET sealed=false`,
		`UPDATE public.content_field_definitions SET visibility='private'`,
	} {
		rejectSQL(t, ctx, db, "", query, "23514")
	}
	rejectSQL(t, ctx, db, languageImporterTestRole, `SELECT * FROM public.content_types`, "42501")
	var missing bool
	requireNoError(t, db.QueryRow(`SELECT to_regclass('public.content_items') IS NULL
AND to_regclass('public.users') IS NULL`).Scan(&missing))
	if !missing {
		t.Fatal("registry step created later domains")
	}
}

func testDomainEngineStep(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO public.content_items(id,type_id) VALUES
('d1000000-0000-0000-0000-000000000003','d1000000-0000-0000-0000-000000000001'),
('d1000000-0000-0000-0000-000000000004','d1000000-0000-0000-0000-000000000001')`)
	requireNoError(t, err)
	for _, query := range []string{
		`UPDATE public.content_items SET id=gen_random_uuid()`,
		`UPDATE public.content_items SET type_id=gen_random_uuid()`,
		`DELETE FROM public.content_items`,
		`UPDATE public.content_items SET published_id=gen_random_uuid()`,
	} {
		rejectSQL(t, ctx, db, "", query, "23514")
	}
	_, err = db.Exec(`INSERT INTO public.languages(code,name,native_name,direction,is_enabled)
VALUES ('en','English','English','ltr',true)`)
	requireNoError(t, err)
	tx := beginRoleTx(t, ctx, db, publisherTestRole)
	execSQL(t, ctx, tx, `INSERT INTO public.content_publications(id,item_id,type_id,schema_no,revision_no,shared_seq)
VALUES ('d1000000-0000-0000-0000-000000000005','d1000000-0000-0000-0000-000000000003','d1000000-0000-0000-0000-000000000001',1,1,1)`)
	_, err = tx.ExecContext(ctx, `UPDATE public.content_items SET published_id='d1000000-0000-0000-0000-000000000005'
WHERE id='d1000000-0000-0000-0000-000000000003'`)
	assertSQLState(t, err, "23514")
	requireNoError(t, tx.Rollback())
	tx = beginRoleTx(t, ctx, db, publisherTestRole)
	execSQL(t, ctx, tx, `INSERT INTO public.content_publications(id,item_id,type_id,schema_no,revision_no,shared_seq)
VALUES ('d1000000-0000-0000-0000-000000000005','d1000000-0000-0000-0000-000000000003','d1000000-0000-0000-0000-000000000001',1,1,1);
INSERT INTO public.content_routes(namespace,locale_code,slug,type_id,item_id)
VALUES ('domain-step','en','step-publication','d1000000-0000-0000-0000-000000000001','d1000000-0000-0000-0000-000000000003');
INSERT INTO public.content_publication_locales(publication_id,item_id,locale_code,locale_generation,slug)
VALUES ('d1000000-0000-0000-0000-000000000005','d1000000-0000-0000-0000-000000000003','en',1,'step-publication');
UPDATE public.content_publications SET sealed=true;
UPDATE public.content_items SET published_id='d1000000-0000-0000-0000-000000000005',version=version+1
WHERE id='d1000000-0000-0000-0000-000000000003'`)
	requireNoError(t, tx.Commit())
	rejectSQL(t, ctx, db, publisherTestRole, `UPDATE public.content_items
SET published_id='d1000000-0000-0000-0000-000000000005'
WHERE id='d1000000-0000-0000-0000-000000000004'`, "23514")
}

func testDomainIdentityStep(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	tx := beginRoleTx(t, ctx, db, securityTestRole)
	execSQL(t, ctx, tx, `INSERT INTO public.users(id,login_key)
VALUES ('d1000000-0000-0000-0000-000000000006','domain-step-pending');
INSERT INTO public.sessions(id,token_digest,csrf_digest,kind,expires_at)
VALUES (gen_random_uuid(),decode(repeat('10',32),'hex'),decode(repeat('11',32),'hex'),'preauth',CURRENT_TIMESTAMP+interval '1 minute');
INSERT INTO public.security_audit(id,operation,reason)
VALUES (gen_random_uuid(),'auth.denied','invalid_credentials')`)
	requireNoError(t, tx.Commit())
	rejectSQL(t, ctx, db, publisherTestRole, `SELECT * FROM public.users`, "42501")
	auditRead := beginRoleTx(t, ctx, db, securityTestRole)
	execSQL(t, ctx, auditRead, `SELECT operation,result,reason FROM public.security_audit`)
	requireNoError(t, auditRead.Commit())
	rejectSQL(t, ctx, db, securityTestRole, `SELECT details FROM public.security_audit`, "42501")
	rejectSQL(t, ctx, db, "", `UPDATE public.security_audit SET reason=''`, "23514")
	rejectSQL(t, ctx, db, "", `DELETE FROM public.security_state`, "23514")
	rejectSQL(t, ctx, db, "", `UPDATE public.users SET state='active'`, "23514")
	var missing bool
	requireNoError(t, db.QueryRow(`SELECT to_regclass('public.roles') IS NULL
AND to_regclass('public.role_grants') IS NULL`).Scan(&missing))
	if !missing {
		t.Fatal("identity step created RBAC domain")
	}
}

func testDomainAssigneeStep(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	const itemID = "d1000000-0000-0000-0000-000000000003"
	const userID = "d1000000-0000-0000-0000-000000000006"
	const attach = `INSERT INTO public.content_item_assignments(item_id,user_id) VALUES($1,$2)`
	tx := beginRoleTx(t, ctx, db, securityTestRole)
	_, err := tx.ExecContext(ctx, attach, itemID, userID)
	requireNoError(t, err)
	var version int64
	err = tx.QueryRowContext(ctx, `UPDATE public.content_items SET version=version+1 WHERE id=$1 RETURNING version`, itemID).Scan(&version)
	requireNoError(t, err)
	requireNoError(t, tx.Commit())
	if version != 3 {
		t.Fatalf("version of published item after assignment = %d, want 3", version)
	}
	for _, test := range []struct {
		itemID, userID, sqlState string
	}{
		{itemID, userID, "23505"},
		{"d1000000-0000-0000-0000-000000000099", userID, "23503"},
		{itemID, "d1000000-0000-0000-0000-000000000099", "23503"},
	} {
		tx = beginRoleTx(t, ctx, db, securityTestRole)
		_, err = tx.ExecContext(ctx, attach, test.itemID, test.userID)
		assertSQLState(t, err, test.sqlState)
		requireNoError(t, tx.Rollback())
	}
}

func TestDomainMigrationsSQLPreflightIntegration(t *testing.T) {
	for _, setup := range []string{
		`CREATE TABLE public.auth_attempt_buckets(foreign_payload text)`,
		`CREATE TABLE public.auth_attempts(foreign_payload text)`,
		`CREATE TABLE public.foreign_fixture(id integer); CREATE INDEX roles_name_key ON public.foreign_fixture(id)`,
		`CREATE TABLE public.foreign_fixture(id integer); CREATE INDEX enrollment_attempts_token_idx ON public.foreign_fixture(id)`,
		`CREATE TYPE public.onboarding_tokens AS ENUM ('foreign')`,
		`CREATE FUNCTION public.guard_content_item_identity() RETURNS integer LANGUAGE sql AS 'SELECT 1'`,
		`CREATE TABLE public.content_item_assignments(foreign_payload text)`,
		`CREATE TABLE public.foreign_fixture(id integer); CREATE INDEX content_item_assignments_pkey ON public.foreign_fixture(id)`,
		`CREATE TABLE public.foreign_fixture(id integer); CREATE INDEX content_item_assignments_user_idx ON public.foreign_fixture(id)`,
		`CREATE TYPE public.content_item_assignments AS (foreign_payload text)`,
		`CREATE TYPE public.content_item_assignments AS ENUM ('foreign')`,
	} {
		t.Run(setup, func(t *testing.T) {
			db, _ := domainQAFixture(t)
			_, err := db.Exec(setup)
			requireNoError(t, err)
			before := domainQANamespaceSnapshot(t, db)
			provider, err := Provider(db)
			requireNoError(t, err)
			_, err = provider.Up(context.Background())
			if err == nil {
				t.Fatal("SQL preflight accepted later-domain namespace conflict")
			}
			var untouched bool
			requireNoError(t, db.QueryRow(`SELECT to_regclass('public.languages') IS NULL
AND NOT EXISTS(SELECT 1 FROM public.goose_db_version WHERE version_id>0 AND is_applied)`).Scan(&untouched))
			if !untouched {
				t.Fatal("SQL preflight allowed earlier domain to commit")
			}
			after := domainQANamespaceSnapshot(t, db)
			if before != after {
				t.Fatal("SQL preflight changed foreign object identities")
			}
		})
	}
}
