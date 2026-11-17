package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

const seoFixtureMode = "seo"

func TestContentRuntimeFixture(t *testing.T) {
	mode := os.Getenv("STAGE32_CONTENT_FIXTURE")
	if mode != "primary" && mode != seoFixtureMode {
		t.Skip("NOT RUN: explicit content runtime fixture mode required")
	}
	db := contentDatabase(t)
	populateRuntimeFixture(t, db, mode)
}

func populateRuntimeFixture(t *testing.T, db *sql.DB, mode string) {
	t.Helper()
	if mode == seoFixtureMode {
		createSEOFixture(t, db)
		return
	}
	prepareContent(t, db)
	s := testService(db)
	ctx := context.Background()
	article := typeID(t, db, initialArticleKey)
	var schema int64
	must(t, db.QueryRow(`SELECT current_schema_no FROM public.content_types WHERE id=$1`, article).Scan(&schema))
	state, err := s.Create(ctx, Actor{}, article, schema)
	must(t, err)
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: schema, Locales: map[string]ValuePatch{"fr": fullLocale("stage15-fr-first"), "ru": fullLocale("stage22-ru-first"), "pl": fullLocale("stage22-pl-hidden")}})
	must(t, err)
	state = readyTestDraft(t, s, state, "fr", "ru", "pl")
	state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, schema, []string{"fr", "ru", "pl"})
	must(t, err)
	_, err = db.Exec(`UPDATE public.languages SET is_enabled=false WHERE code='pl'`)
	must(t, err)
	fr := fullLocale("stage15-fr-current")
	fr.Values[titleFieldKey] = stringValue("Bonjour de Stepolonia")
	body, _ := json.Marshal("## Une publication\n\nPublié **en français**, avec un [lien sûr](https://example.org).\n\n[JavaScript](javascript:alert(1))")
	fr.Values[bodyFieldKey] = FieldValue{kindMarkdown, body}
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: schema, Locales: map[string]ValuePatch{"fr": fr, "ru": fullLocale("stage22-ru-current")}})
	must(t, err)
	state = readyTestDraft(t, s, state, "fr", "ru")
	state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, schema, []string{"fr", "ru"})
	must(t, err)
	_, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: schema, Locales: map[string]ValuePatch{"ru": fullLocale("stage22-ru-draft")}})
	must(t, err)
	createEventRuntimeFixture(t, s)
}
func createEventRuntimeFixture(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	input := initialType()
	input.Key = eventTypeKey
	input.RouteNamespace = eventsNamespace
	input.Fields = append(input.Fields, Definition{Key: featuredFieldKey, Kind: kindBoolean, Scope: scopeShared, Visibility: visibilityPublic}, Definition{Key: "price", Kind: kindDecimal, Scope: scopeShared, Visibility: visibilityPublic}, Definition{Key: "secret", Kind: kindString, Scope: scopeShared, Visibility: visibilityPrivate})
	schema, err := s.CreateType(ctx, Actor{}, input)
	must(t, err)
	state := createTestDraft(t, s, schema.TypeID, map[string]ValuePatch{"fr": fullLocale("stage32-event")})
	title := ValuePatch{Values: map[string]FieldValue{titleFieldKey: stringValue("Stage 3.2 Event")}}
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Shared: map[string]FieldValue{featuredFieldKey: {kindBoolean, json.RawMessage(`true`)}, "price": {kindDecimal, json.RawMessage(`"123456789012.123456"`)}, "secret": stringValue("private-event-secret")}, Locales: map[string]ValuePatch{"fr": title}})
	must(t, err)
	state = readyTestDraft(t, s, state, "fr")
	_, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"fr"})
	must(t, err)
}

func createSEOFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO public.languages(code,name,native_name,direction,is_enabled) VALUES('qaa','Private test','Private test','ltr',true),('en-us','English US','English US','ltr',true),('zh-hans-cn','Chinese','中文','ltr',true) ON CONFLICT(code) DO UPDATE SET is_enabled=true`)
	must(t, err)
	s := testService(db)
	article := typeID(t, db, initialArticleKey)
	for index := 1; index <= 21; index++ {
		slug := "seo-fixture-" + strconv.Itoa(index)
		locales := map[string]ValuePatch{"fr": fullLocale(slug)}
		intended := []string{"fr"}
		if index == 21 {
			locales["qaa"] = fullLocale(slug)
			intended = append(intended, "qaa")
		}
		for code, patch := range locales {
			patch.Values[titleFieldKey] = stringValue("SEO fixture " + strconv.Itoa(index))
			locales[code] = patch
		}
		state := createTestDraft(t, s, article, locales)
		state = readyTestDraft(t, s, state, intended...)
		_, err := s.Publish(context.Background(), Actor{}, state.ItemID, state.Version, state.SchemaNo, intended)
		must(t, err)
	}
}

func TestContentSEOFixtureIsolation(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	_, err := db.Exec(`UPDATE public.languages SET is_enabled=false WHERE code IN ('ru','kk')`)
	must(t, err)
	populateRuntimeFixture(t, db, seoFixtureMode)
	var preserved bool
	must(t, db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM public.languages WHERE code IN ('ru','kk') AND is_enabled) AND (SELECT count(*)=21 FROM public.public_content_items WHERE language_code='fr')`).Scan(&preserved))
	if !preserved {
		t.Fatal("SEO fixture changed an existing installation's language state")
	}
	var title string
	must(t, db.QueryRow(`SELECT localized_values->>'title' FROM public.public_content_items WHERE slug='seo-fixture-21' AND language_code='fr'`).Scan(&title))
	if title != "SEO fixture 21" {
		t.Fatal("SEO fixture did not preserve its content contract")
	}
}
