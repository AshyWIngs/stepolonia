package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const qaLocale = "ru"
const qaCustom = "qa_custom"
const qaOldestSlug = "qa-oldest"
const qaSecretField = "qa_secret"

func qaRuntime(t *testing.T, owner *sql.DB) *sql.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("STAGE12_TEST_DATABASE_URL"))
	must(t, err)
	raw, err := readFixturePassword(os.Getenv("STAGE12_TEST_PASSWORD_FILE"))
	must(t, err)
	cfg.Password = strings.TrimSpace(string(raw))
	var name string
	must(t, owner.QueryRow(`SELECT current_database()`).Scan(&name))
	cfg.Database = name
	id, err := newID()
	must(t, err)
	role := "qa_content_" + strings.ReplaceAll(id, "-", "")
	password, err := newID()
	must(t, err)
	_, err = owner.Exec("CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " LOGIN PASSWORD '" + password + "'")
	must(t, err)
	for _, q := range []string{
		"REVOKE CREATE,TEMP ON DATABASE " + pgx.Identifier{name}.Sanitize() + " FROM PUBLIC",
		"GRANT USAGE ON SCHEMA public TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT ON public.public_languages,public.public_content_types,public.public_content_items,public.public_content_routes,public.public_content_relations TO " + pgx.Identifier{role}.Sanitize(),
	} {
		_, err = owner.Exec(q)
		must(t, err)
	}
	cfg.User = role
	cfg.Password = password
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = owner.Exec("DROP OWNED BY " + pgx.Identifier{role}.Sanitize())
		_, _ = owner.Exec("DROP ROLE " + pgx.Identifier{role}.Sanitize())
	})
	must(t, db.Ping())
	return db
}

func qaPublish(t *testing.T, s *Service, state DraftState, locales ...string) DraftState {
	t.Helper()
	state = readyTestDraft(t, s, state, locales...)
	state, err := s.Publish(context.Background(), Actor{}, state.ItemID, state.Version, state.SchemaNo, locales)
	must(t, err)
	return state
}

func TestContentIndependentDisplayAndSemanticIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	runtime := qaRuntime(t, db)
	article, err := s.GetSchema(context.Background(), Actor{}, typeID(t, db, articleTypeKey), 1)
	must(t, err)
	input := initialType()
	input.Key = eventTypeKey
	input.RouteNamespace = eventsNamespace
	event, err := s.CreateType(context.Background(), Actor{}, input)
	must(t, err)
	for _, schema := range []TypeSchema{article, event} {
		t.Run(schema.Key, func(t *testing.T) { qaDisplayAndSemantic(t, s, runtime, schema) })
	}
}

func qaDisplayAndSemantic(t *testing.T, s *Service, runtime *sql.DB, schema TypeSchema) {
	t.Helper()
	ctx := context.Background()
	state := createTestDraft(t, s, schema.TypeID, map[string]ValuePatch{qaLocale: fullLocale("qa-display")})
	state = qaPublish(t, s, state, qaLocale)
	first := state.PublicationID
	state = readyTestDraft(t, s, state, qaLocale)
	before := state
	for _, metadata := range []string{`{"ru":{"help":"New help"}}`, `{"ru":{"label":"New label"}}`} {
		schema = qaDisplayEdit(t, s, schema, metadata)
		after, err := s.GetDraft(ctx, Actor{}, state.ItemID)
		must(t, err)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("display-only change modified draft counters or readiness")
		}
	}
	state, err := s.Publish(ctx, Actor{}, state.ItemID, state.Version, state.SchemaNo, []string{qaLocale})
	must(t, err)
	var oldLabels, newLabels string
	must(t, s.db.QueryRow(`SELECT display_metadata::text FROM public.content_publications WHERE id=$1`, first).Scan(&oldLabels))
	must(t, s.db.QueryRow(`SELECT display_metadata::text FROM public.content_publications WHERE id=$1`, state.PublicationID).Scan(&newLabels))
	if strings.Contains(oldLabels, "New label") || !strings.Contains(newLabels, "New label") {
		t.Fatal("publication display metadata was live or not captured")
	}
	input := TypeInput{schema.Key, schema.RouteNamespace, append(schema.Fields, Definition{Key: qaCustom, Kind: kindInteger, Scope: scopeShared, Visibility: visibilityPublic})}
	next, err := s.ReviseType(ctx, Actor{}, schema.TypeID, schema.Version, input)
	must(t, err)
	_, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, state.SchemaNo, []string{qaLocale})
	errorCode(t, err, schemaConflictCode)
	state, err = s.AdoptSchema(ctx, Actor{}, state.ItemID, state.Version, next.SchemaNo)
	must(t, err)
	if state.SharedReadySeq.Valid || state.Locales[qaLocale].Ready != nil {
		t.Fatal("semantic adoption retained ready")
	}
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: next.SchemaNo, Shared: map[string]FieldValue{qaCustom: {kindInteger, json.RawMessage(`"-9223372036854775808"`)}}})
	must(t, err)
	state = qaPublish(t, s, state, qaLocale)
	public, err := NewPublicReader(runtime).Resolve(ctx, schema.Key, qaLocale, "qa-display")
	must(t, err)
	if public.Item.SchemaNo != "2" || string(public.Item.Shared[qaCustom].Value) != `"-9223372036854775808"` {
		t.Fatal("public schema or int64 lost precision")
	}
	qaRawImmutability(t, s, state)
	qaUnsealedCommits(t, s.db, state)
}

func qaDisplayEdit(t *testing.T, s *Service, schema TypeSchema, metadata string) TypeSchema {
	t.Helper()
	input := TypeInput{schema.Key, schema.RouteNamespace, schema.Fields}
	input.Fields[0].DisplayLabels = json.RawMessage(metadata)
	updated, err := s.ReviseType(context.Background(), Actor{}, schema.TypeID, schema.Version, input)
	must(t, err)
	if updated.SchemaNo != schema.SchemaNo || updated.Version <= schema.Version {
		t.Fatal("display edit did not preserve semantic version")
	}
	return updated
}

func qaRawImmutability(t *testing.T, s *Service, state DraftState) {
	t.Helper()
	other, err := s.Create(context.Background(), Actor{}, state.TypeID, state.SchemaNo)
	must(t, err)
	cases := []struct {
		query string
		args  []any
	}{
		{`UPDATE public.content_publications SET sealed=false WHERE id=$1`, []any{state.PublicationID}},
		{`UPDATE public.content_publication_locales SET scalar_values='{}' WHERE publication_id=$1`, []any{state.PublicationID}},
		{`DELETE FROM public.content_publication_locales WHERE publication_id=$1`, []any{state.PublicationID}},
		{`UPDATE public.content_routes SET slug='stolen' WHERE item_id=$1`, []any{state.ItemID}},
		{`UPDATE public.content_items SET published_id=$1 WHERE id=$2`, []any{state.PublicationID, other.ItemID}},
	}
	for i, check := range cases {
		tx, err := s.db.Begin()
		must(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`SET LOCAL ROLE sp_publisher`)
		must(t, err)
		_, err = tx.Exec(check.query, check.args...)
		_ = tx.Rollback()
		if err == nil {
			t.Fatalf("publisher changed sealed or foreign publication, query %d", i)
		}
	}
}

func TestContentIndependentRuntimePrivacyAndAliasesIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	runtime := qaRuntime(t, db)
	ctx := context.Background()
	target := createTestDraft(t, s, typeID(t, db, articleTypeKey), map[string]ValuePatch{qaLocale: fullLocale("qa-target")})
	target = qaPublish(t, s, target, qaLocale)
	schema := qaRelationType(t, s, target.TypeID)
	source := createTestDraft(t, s, schema.TypeID, map[string]ValuePatch{qaLocale: fullLocale(qaOldestSlug)})
	relation := Relation{FieldID: qaFieldID(schema, "qa_relation"), TargetTypeID: target.TypeID, TargetItemID: target.ItemID}
	source, err := s.SaveDraft(ctx, Actor{}, source.ItemID, source.Version, DraftPatch{SchemaNo: 1, Shared: map[string]FieldValue{qaSecretField: stringValue("qa-confidential-value")}, ReplaceRelations: true, Relations: []Relation{relation}})
	must(t, err)
	source = qaPublish(t, s, source, qaLocale)
	reader := NewPublicReader(runtime)
	visible, err := reader.Resolve(ctx, schema.Key, qaLocale, qaOldestSlug)
	must(t, err)
	if len(visible.Item.Relations) != 1 {
		t.Fatal("public relation missing")
	}
	for _, slug := range []string{"qa-middle", "qa-current"} {
		source, err = s.SaveDraft(ctx, Actor{}, source.ItemID, source.Version, DraftPatch{SchemaNo: 1, Locales: map[string]ValuePatch{qaLocale: {Values: map[string]FieldValue{slugFieldKey: stringValue(slug)}}}})
		must(t, err)
		source = qaPublish(t, s, source, qaLocale)
	}
	for _, slug := range []string{qaOldestSlug, "qa-middle"} {
		result, err := reader.Resolve(ctx, schema.Key, qaLocale, slug)
		must(t, err)
		if !result.Redirect || result.Item.CanonicalURL != "/ru/events/qa-current" {
			t.Fatal("alias did not resolve directly to latest slug")
		}
	}
	_, err = s.Unpublish(ctx, Actor{}, target.ItemID, target.Version, target.SchemaNo)
	must(t, err)
	visible, err = reader.Resolve(ctx, schema.Key, qaLocale, "qa-current")
	must(t, err)
	encoded, err := json.Marshal(visible.Item)
	must(t, err)
	if len(visible.Item.Relations) != 0 || strings.Contains(string(encoded), "qa-confidential") || strings.Contains(string(encoded), qaSecretField) || strings.Contains(string(encoded), target.ItemID) {
		t.Fatal("runtime leaked private scalar or unpublished relation identity")
	}
	qaRuntimeIsolation(t, runtime)
	_, err = s.Unpublish(ctx, Actor{}, source.ItemID, source.Version, source.SchemaNo)
	must(t, err)
	_, err = reader.Resolve(ctx, schema.Key, qaLocale, qaOldestSlug)
	errorCode(t, err, "not_found")
}

func qaRelationType(t *testing.T, s *Service, targetType string) TypeSchema {
	t.Helper()
	input := initialType()
	input.Key = eventTypeKey
	input.RouteNamespace = eventsNamespace
	input.Fields = append(input.Fields, Definition{Key: "qa_relation", Kind: kindRelation, Scope: scopeShared, Visibility: visibilityPublic, TargetTypeID: targetType}, Definition{Key: qaSecretField, Kind: kindString, Scope: scopeShared, Visibility: visibilityPrivate})
	schema, err := s.CreateType(context.Background(), Actor{}, input)
	must(t, err)
	return schema
}

func qaFieldID(schema TypeSchema, key string) string {
	for _, field := range schema.Fields {
		if field.Key == key {
			return field.ID
		}
	}
	return ""
}

func qaRuntimeIsolation(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT shared_values FROM public.content_drafts`,
		`SELECT display_metadata FROM public.content_publications`,
		`SELECT config FROM public.content_field_definitions`,
		`SELECT password_hash FROM public.users`,
		`CREATE TABLE public.qa_forbidden(id int)`,
		`CREATE TEMP TABLE qa_forbidden(id int)`,
		`SELECT public.guard_content_publication()`,
	} {
		_, err := db.Exec(query)
		if err == nil {
			t.Fatalf("runtime unexpectedly allowed %s", query)
		}
	}
}

func TestContentIndependentPublishDisableRaceIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	ctx := context.Background()
	state := createTestDraft(t, s, typeID(t, db, articleTypeKey), map[string]ValuePatch{qaLocale: fullLocale("qa-disable-race")})
	state = readyTestDraft(t, s, state, qaLocale)
	tx, err := db.Begin()
	must(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`UPDATE public.languages SET is_enabled=false WHERE code='ru'`)
	must(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := s.Publish(ctx, Actor{}, state.ItemID, state.Version, state.SchemaNo, []string{qaLocale})
		done <- err
	}()
	qaWaitForWriter(t, db)
	must(t, tx.Commit())
	err = <-done
	var domain *Error
	if !errors.As(err, &domain) || domain.Kind != Conflict {
		t.Fatalf("publish did not recheck disabled locale after mutex: %v", err)
	}
	var unchanged bool
	must(t, db.QueryRow(`SELECT version=$2 AND published_id IS NULL AND NOT EXISTS(SELECT 1 FROM public.content_publications WHERE item_id=$1) FROM public.content_items WHERE id=$1`, state.ItemID, state.Version).Scan(&unchanged))
	if !unchanged {
		t.Fatal("failed publish left a snapshot or advanced candidate")
	}
}

func qaWaitForWriter(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&waiting)
		must(t, err)
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("publish did not wait for language transaction")
		case <-ticker.C:
		}
	}
}

func qaUnsealedCommits(t *testing.T, db *sql.DB, state DraftState) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,$2)`,
		`INSERT INTO public.content_publications(id,item_id,type_id,schema_no,revision_no,shared_seq,shared_values,display_metadata) VALUES(gen_random_uuid(),$3,$1,$2-1,100,1,'{}','{}')`,
	} {
		tx, err := db.Begin()
		must(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`SET LOCAL ROLE sp_publisher`)
		must(t, err)
		if strings.Contains(query, "$3") {
			_, err = tx.Exec(query, state.TypeID, state.SchemaNo+1, state.ItemID)
		} else {
			_, err = tx.Exec(query, state.TypeID, state.SchemaNo+1)
		}
		must(t, err)
		if err = tx.Commit(); err == nil {
			t.Fatal("publisher committed an unsealed schema or snapshot")
		}
	}
}
