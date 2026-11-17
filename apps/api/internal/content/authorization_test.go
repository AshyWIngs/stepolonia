package content

import (
	"context"
	"database/sql"
	"testing"
)

func TestContentAuthorizationIntegration(t *testing.T) {
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	ctx := context.Background()
	article := typeID(t, db, initialArticleKey)
	state := createTestDraft(t, s, article, map[string]ValuePatch{"ru": fullLocale("authorized")})
	other := createTestDraft(t, s, article, map[string]ValuePatch{"ru": fullLocale("hidden")})
	s.authorize = func(_ context.Context, _ *sql.Tx, _ Actor, operation, typeID, item string, fields []string) error {
		if item == other.ItemID {
			return problem(Forbidden, string(Forbidden))
		}
		for _, field := range fields {
			if field == bodyFieldKey {
				return problem(Forbidden, string(Forbidden))
			}
		}
		return nil
	}
	_, err := s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Locales: map[string]ValuePatch{"ru": {Values: map[string]FieldValue{bodyFieldKey: {kindMarkdown, []byte(`"Denied"`)}}}}})
	errorCode(t, err, string(Forbidden))
	s.scope = func(context.Context, *sql.Tx, Actor, string) (itemScope, error) {
		return itemScope{ItemIDs: []string{state.ItemID}}, nil
	}
	visible, err := s.GetDraft(ctx, Actor{}, state.ItemID)
	must(t, err)
	if _, exists := visible.Locales["ru"].Values[bodyFieldKey]; exists {
		t.Fatal("forbidden field escaped projection")
	}
	list, err := s.ListDrafts(ctx, Actor{}, article, 1, 0)
	must(t, err)
	if len(list) != 1 || list[0].ItemID != state.ItemID {
		t.Fatal("item permission was applied after pagination")
	}
	_, err = s.GetDraft(ctx, Actor{}, other.ItemID)
	errorCode(t, err, string(Forbidden))
}
