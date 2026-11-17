package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const testTokenEnv = "DB_DISPOSABLE_TOKEN"

func contentDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("STAGE12_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT RUN: disposable PostgreSQL is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	must(t, err)
	if !strings.HasPrefix(cfg.Database, "stage12_") || (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") {
		t.Fatal("refusing non-loopback disposable database")
	}
	if path := os.Getenv("STAGE12_TEST_PASSWORD_FILE"); path != "" {
		raw, err := readFixturePassword(path)
		must(t, err)
		cfg.Password = strings.TrimSpace(string(raw))
	}
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	token := os.Getenv(testTokenEnv)
	if token == "" {
		t.Fatal("disposable marker token required")
	}
	var marker string
	must(t, db.QueryRow(`SELECT COALESCE(pg_catalog.shobj_description(oid,'pg_database'),'') FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&marker))
	if marker != "stepolonia:disposable:"+token {
		t.Fatal("disposable marker mismatch")
	}
	if strings.HasPrefix(t.Name(), "TestContent") && t.Name() != "TestContentRuntimeFixture" {
		return isolatedContentDatabase(t, db, cfg)
	}
	return db
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func testService(db *sql.DB) *Service {
	s := NewService(db)
	s.scope = func(context.Context, *sql.Tx, Actor, string) (itemScope, error) { return itemScope{Any: true}, nil }
	s.authorize = func(context.Context, *sql.Tx, Actor, string, string, string, []string) error { return nil }
	return s
}
func prepareContent(t *testing.T, db *sql.DB) {
	t.Helper()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../database/migrations"))
	must(t, err)
	_, err = provider.Up(context.Background())
	must(t, err)
	must(t, SeedInitialRegistry(context.Background(), db))
	_, err = db.Exec(`INSERT INTO public.languages(code,name,native_name,direction,is_enabled) VALUES('ru','Russian','Русский','ltr',true),('en','English','English','ltr',true),('pl','Polish','Polski','ltr',true),('fr','French','Français','ltr',true) ON CONFLICT(code) DO UPDATE SET is_enabled=true`)
	must(t, err)
}
func typeID(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var id string
	must(t, db.QueryRow(`SELECT id::text FROM public.content_types WHERE key=$1`, key).Scan(&id))
	return id
}
func stringValue(value string) FieldValue {
	raw, _ := json.Marshal(value)
	return FieldValue{kindString, raw}
}
func fullLocale(slug string) ValuePatch {
	body, _ := json.Marshal("A safe **published** body.")
	return ValuePatch{Values: map[string]FieldValue{titleFieldKey: stringValue("Published " + slug), slugFieldKey: stringValue(slug), bodyFieldKey: {kindMarkdown, body}}}
}
func createTestDraft(t *testing.T, s *Service, typeID string, locales map[string]ValuePatch) DraftState {
	t.Helper()
	ctx := context.Background()
	state, err := s.Create(ctx, Actor{}, typeID, 1)
	must(t, err)
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Locales: locales})
	must(t, err)
	return state
}
func readyTestDraft(t *testing.T, s *Service, state DraftState, locales ...string) DraftState {
	t.Helper()
	ctx := context.Background()
	state, err := s.MarkSharedReady(ctx, Actor{}, state.ItemID, state.Version, state.SchemaNo)
	must(t, err)
	for _, locale := range locales {
		state, err = s.MarkReady(ctx, Actor{}, state.ItemID, locale, state.Version, state.SchemaNo)
		must(t, err)
	}
	return state
}
func errorCode(t *testing.T, err error, code string) {
	t.Helper()
	var domain *Error
	if !errors.As(err, &domain) || domain.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func TestContentIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	article := typeID(t, db, initialArticleKey)
	_, err := NewService(db).Create(ctx, Actor{}, article, 1)
	errorCode(t, err, string(Forbidden))
	state := createTestDraft(t, s, article, map[string]ValuePatch{"ru": fullLocale("core-ru"), "pl": fullLocale("core-pl")})
	state = readyTestDraft(t, s, state, "ru", "pl")
	state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru", "pl"})
	must(t, err)
	t.Run("partial publication copies prior translation", func(t *testing.T) {
		state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Locales: map[string]ValuePatch{"ru": fullLocale("core-ru-next"), "pl": fullLocale("unready-pl")}})
		must(t, err)
		state = readyTestDraft(t, s, state, "ru")
		state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru"})
		must(t, err)
		var slug string
		must(t, db.QueryRow(`SELECT slug FROM public.content_publication_locales WHERE publication_id=$1 AND locale_code='pl'`, state.PublicationID).Scan(&slug))
		if slug != "core-pl" {
			t.Fatal("copied incomplete draft")
		}
	})
	t.Run("version and immutable snapshots", func(t *testing.T) {
		_, err := s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version-1, DraftPatch{SchemaNo: 1})
		errorCode(t, err, versionConflictCode)
		_, err = db.Exec(`UPDATE public.content_publications SET shared_values='{}' WHERE id=$1`, state.PublicationID)
		if err == nil {
			t.Fatal("sealed snapshot was mutable")
		}
	})
	t.Run("display and adoption", func(t *testing.T) { testSchemaAdoption(t, s, db, state) })
}
func testSchemaAdoption(t *testing.T, s *Service, db *sql.DB, state DraftState) {
	ctx := context.Background()
	schema, err := s.GetSchema(ctx, Actor{}, state.TypeID, 0)
	must(t, err)
	input := TypeInput{schema.Key, schema.RouteNamespace, schema.Fields}
	input.Fields[0].DisplayLabels = []byte(`{"ru":{"label":"Updated"}}`)
	updated, err := s.ReviseType(ctx, Actor{}, schema.TypeID, schema.Version, input)
	must(t, err)
	if updated.SchemaNo != schema.SchemaNo {
		t.Fatal("display change revised semantic schema")
	}
	input.Fields = append(input.Fields, Definition{Key: "custom", Kind: kindBoolean, Scope: scopeShared, Visibility: visibilityPublic})
	updated, err = s.ReviseType(ctx, Actor{}, schema.TypeID, updated.Version, input)
	must(t, err)
	_, err = s.MarkSharedReady(ctx, Actor{}, state.ItemID, state.Version, 1)
	errorCode(t, err, schemaConflictCode)
	previousPublication := state.PublicationID
	unpublished, err := s.Unpublish(ctx, Actor{}, state.ItemID, state.Version, state.SchemaNo)
	must(t, err)
	state = unpublished
	adopted, err := s.AdoptSchema(ctx, Actor{}, state.ItemID, state.Version, updated.SchemaNo)
	must(t, err)
	if adopted.SharedReadySeq.Valid || adopted.Locales["ru"].Ready != nil {
		t.Fatal("adoption retained ready")
	}
	var before string
	must(t, db.QueryRow(`SELECT slug FROM public.content_publication_locales WHERE publication_id=$1 AND locale_code='ru'`, previousPublication).Scan(&before))
	if before != "core-ru-next" {
		t.Fatal("schema changed old publication")
	}
}

func isolatedContentDatabase(t *testing.T, admin *sql.DB, cfg *pgx.ConnConfig) *sql.DB {
	t.Helper()
	suffix, err := newID()
	must(t, err)
	name := "stage12_content_" + strings.ReplaceAll(suffix, "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	_, err = admin.Exec("CREATE DATABASE " + quoted)
	must(t, err)
	child := cfg.Copy()
	child.Database = name
	db := stdlib.OpenDB(*child)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() {
		_ = db.Close()
		if _, err := admin.Exec("DROP DATABASE " + quoted + " WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	return db
}

func readFixturePassword(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}
