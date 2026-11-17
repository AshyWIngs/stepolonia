package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestContentD4Integration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	ctx := context.Background()
	input := initialType()
	input.Key = eventTypeKey
	input.RouteNamespace = eventsNamespace
	input.Fields = append(input.Fields, Definition{Key: fixtureFeaturedFieldKey, Kind: kindBoolean, Scope: scopeShared, Visibility: visibilityPublic})
	schema, err := s.CreateType(ctx, Actor{}, input)
	must(t, err)
	state := createTestDraft(t, s, schema.TypeID, map[string]ValuePatch{"ru": fullLocale("d4-ru"), "pl": fullLocale("d4-pl")})
	state = readyTestDraft(t, s, state, "ru", "pl")
	state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru", "pl"})
	must(t, err)
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Shared: map[string]FieldValue{fixtureFeaturedFieldKey: {kindBoolean, json.RawMessage(`true`)}}})
	must(t, err)
	state = readyTestDraft(t, s, state, "ru")
	_, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru"})
	errorCode(t, err, notReadyCode)
	_, err = db.Exec(`UPDATE public.languages SET is_enabled=false WHERE code='pl'`)
	must(t, err)
	state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru"})
	must(t, err)
	_, err = db.Exec(`UPDATE public.languages SET is_enabled=true WHERE code='pl'`)
	must(t, err)
	assertPublicLocaleAbsent(t, db, state.ItemID, "pl")
	_, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"pl"})
	errorCode(t, err, notReadyCode)
	state = readyTestDraft(t, s, state, "pl")
	state, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"pl"})
	must(t, err)
	state, err = s.Unpublish(ctx, Actor{}, state.ItemID, state.Version, 1)
	must(t, err)
	_, err = s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru"})
	errorCode(t, err, notReadyCode)
}
func assertPublicLocaleAbsent(t *testing.T, db *sql.DB, item, locale string) {
	t.Helper()
	var count int
	must(t, db.QueryRow(`SELECT count(*) FROM public.public_content_items WHERE item_id=$1 AND language_code=$2`, item, locale).Scan(&count))
	if count != 0 {
		t.Fatal("stale generation was publicly visible")
	}
}
func TestContentConcurrentEditsIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	state := createTestDraft(t, s, typeID(t, db, articleTypeKey), map[string]ValuePatch{"ru": fullLocale("race")})
	results := make(chan error, 2)
	for _, title := range []string{"First", "Second"} {
		go func(value string) {
			_, err := s.SaveDraft(context.Background(), Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Locales: map[string]ValuePatch{"ru": {Values: map[string]FieldValue{titleFieldKey: stringValue(value)}}}})
			results <- err
		}(title)
	}
	first, second := <-results, <-results
	if first == nil && second == nil || first != nil && second != nil {
		t.Fatalf("expected one winning edit: %v, %v", first, second)
	}
	if first != nil {
		errorCode(t, first, versionConflictCode)
	} else {
		errorCode(t, second, versionConflictCode)
	}
}
func TestContentRelationsIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	ctx := context.Background()
	article := typeID(t, db, articleTypeKey)
	target := createTestDraft(t, s, article, map[string]ValuePatch{"ru": fullLocale("relation-target")})
	target = readyTestDraft(t, s, target, "ru")
	target, err := s.Publish(ctx, Actor{}, target.ItemID, target.Version, 1, []string{"ru"})
	must(t, err)
	input := initialType()
	input.Key = eventTypeKey
	input.RouteNamespace = eventsNamespace
	input.Fields = append(input.Fields, Definition{Key: "related", Kind: kindRelation, Scope: scopeShared, Visibility: visibilityPublic, TargetTypeID: article})
	schema, err := s.CreateType(ctx, Actor{}, input)
	must(t, err)
	source := createTestDraft(t, s, schema.TypeID, map[string]ValuePatch{"ru": fullLocale("relation-source")})
	var relationID string
	for _, field := range schema.Fields {
		if field.Key == "related" {
			relationID = field.ID
		}
	}
	source, err = s.SaveDraft(ctx, Actor{}, source.ItemID, source.Version, DraftPatch{SchemaNo: 1, ReplaceRelations: true, Relations: []Relation{{relationID, "", article, target.ItemID}}})
	must(t, err)
	source = readyTestDraft(t, s, source, "ru")
	source, err = s.Publish(ctx, Actor{}, source.ItemID, source.Version, 1, []string{"ru"})
	must(t, err)
	_, err = s.Unpublish(ctx, Actor{}, target.ItemID, target.Version, 1)
	must(t, err)
	var count int
	must(t, db.QueryRow(`SELECT count(*) FROM public.public_content_relations WHERE publication_id=$1`, source.PublicationID).Scan(&count))
	if count != 0 {
		t.Fatal("unpublished target leaked relation")
	}
	_, err = s.Publish(ctx, Actor{}, source.ItemID, source.Version, 1, []string{"ru"})
	errorCode(t, err, "relation_unavailable")
	assertRelationProjection(t, s, source, target)
}

func assertRelationProjection(t *testing.T, s *Service, source, target DraftState) {
	t.Helper()
	s.authorize = func(_ context.Context, _ *sql.Tx, _ Actor, _ string, _ string, item string, _ []string) error {
		if item == target.ItemID {
			return problem(Forbidden, string(Forbidden))
		}
		return nil
	}
	visible, err := s.GetDraft(context.Background(), Actor{}, source.ItemID)
	must(t, err)
	if len(visible.Relations) != 0 {
		t.Fatal("unreadable target identity leaked through editorial relation")
	}
}
