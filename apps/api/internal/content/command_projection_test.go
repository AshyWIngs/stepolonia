package content

import (
	"context"
	"database/sql"
	"stepolonia/apps/api/internal/cms"
	"testing"
)

const adoptCommand = "adopt"
const privateNoteKey = "private_note"
const hiddenLinkKey = "hidden_link"

func TestContentCommandProjectionIntegration(t *testing.T) {
	for _, command := range []string{"save", "ready", "shared-ready", adoptCommand, "adopt-noop", "unpublish", "publish"} {
		t.Run(command, func(t *testing.T) { testCommandProjection(t, command) })
	}
}
func testCommandProjection(t *testing.T, command string) {
	t.Helper()
	db := contentDatabase(t)
	prepareContent(t, db)
	s := testService(db)
	ctx := context.Background()
	input := initialType()
	input.Key = eventTypeKey
	input.RouteNamespace = eventsNamespace
	target := createTestDraft(t, s, typeID(t, db, articleTypeKey), map[string]ValuePatch{"ru": fullLocale("hidden-target")})
	input.Fields = append(input.Fields, Definition{Key: privateNoteKey, Kind: kindString, Scope: scopeShared, Visibility: visibilityPrivate}, Definition{Key: hiddenLinkKey, Kind: kindRelation, Scope: scopeShared, Visibility: visibilityPrivate, TargetTypeID: target.TypeID})
	schema, err := s.CreateType(ctx, Actor{}, input)
	must(t, err)
	state := createTestDraft(t, s, schema.TypeID, map[string]ValuePatch{"ru": fullLocale("command-projection")})
	state, err = s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Shared: map[string]FieldValue{privateNoteKey: stringValue("must-not-escape")}})
	must(t, err)
	state = addHiddenLink(t, s, state, schema, target)
	state = readyTestDraft(t, s, state, "ru")
	current := int64(1)
	if command == adoptCommand {
		input.Fields = append(schema.Fields, Definition{Key: "optional", Kind: kindBoolean, Scope: scopeShared, Visibility: visibilityPublic})
		schema, err = s.ReviseType(ctx, Actor{}, schema.TypeID, schema.Version, input)
		must(t, err)
		current = schema.SchemaNo
	}
	s.authorize = denyPrivateRead
	visible, err := runProjectionCommand(ctx, s, state, current, command)
	must(t, err)
	if _, exists := visible.Shared[privateNoteKey]; exists {
		t.Fatal("command response disclosed unreadable private value")
	}
	if len(visible.Relations) != 0 {
		t.Fatal("command response disclosed unreadable relation target")
	}
	if visible.ItemID != state.ItemID {
		t.Fatal("command did not return its actor-visible state")
	}
	var stored string
	must(t, db.QueryRow(`SELECT shared_values->>'private_note' FROM public.content_drafts WHERE item_id=$1`, state.ItemID).Scan(&stored))
	if stored != "must-not-escape" {
		t.Fatal("projection altered stored values")
	}
}
func denyPrivateRead(_ context.Context, _ *sql.Tx, _ Actor, operation, _, _ string, fields []string) error {
	if operation != string(cms.OpContentRead) {
		return nil
	}
	for _, field := range fields {
		if field == privateNoteKey || field == hiddenLinkKey {
			return problem(Forbidden, string(Forbidden))
		}
	}
	return nil
}
func runProjectionCommand(ctx context.Context, s *Service, state DraftState, current int64, command string) (DraftState, error) {
	switch command {
	case "save":
		return s.SaveDraft(ctx, Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, Locales: map[string]ValuePatch{"ru": {Values: map[string]FieldValue{titleFieldKey: stringValue("Changed title")}}}})
	case "ready":
		return s.MarkReady(ctx, Actor{}, state.ItemID, "ru", state.Version, 1)
	case "shared-ready":
		return s.MarkSharedReady(ctx, Actor{}, state.ItemID, state.Version, 1)
	case adoptCommand, "adopt-noop":
		return s.AdoptSchema(ctx, Actor{}, state.ItemID, state.Version, current)
	case "publish":
		return s.Publish(ctx, Actor{}, state.ItemID, state.Version, 1, []string{"ru"})
	default:
		return s.Unpublish(ctx, Actor{}, state.ItemID, state.Version, 1)
	}
}

func addHiddenLink(t *testing.T, s *Service, state DraftState, schema TypeSchema, target DraftState) DraftState {
	t.Helper()
	var fieldID string
	for _, field := range schema.Fields {
		if field.Key == hiddenLinkKey {
			fieldID = field.ID
		}
	}
	state, err := s.SaveDraft(context.Background(), Actor{}, state.ItemID, state.Version, DraftPatch{SchemaNo: 1, ReplaceRelations: true, Relations: []Relation{{fieldID, "", target.TypeID, target.ItemID}}})
	must(t, err)
	return state
}
