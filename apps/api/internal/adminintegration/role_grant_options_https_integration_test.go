package adminintegration

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type grantOptionView struct {
	Operation   string                 `json:"operation"`
	Scope       string                 `json:"scope"`
	CanDelegate bool                   `json:"can_delegate"`
	TypeID      string                 `json:"type_id,omitempty"`
	TypeKey     string                 `json:"type_key,omitempty"`
	ObjectScope string                 `json:"object_scope,omitempty"`
	Fields      []grantOptionFieldView `json:"fields,omitempty"`
}

type grantOptionFieldView struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func TestAdminRoleGrantOptionsHTTPSIntegration(t *testing.T) {
	x := newAssigneeFixture(t)
	const endpoint = rolesHTTPPath + "/grant-options"
	x.http.lifecycleRead(t, endpoint, x.actor, http.StatusForbidden)
	seedGrantOptionFields(t, x)

	raw := sendDraftRequest(t, x.http.server, http.MethodGet, x.actor.Token, draftHTTPRequest{path: endpoint, status: http.StatusOK})
	var out struct {
		Options []grantOptionView `json:"options"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	requireNoError(t, decoder.Decode(&out))
	expected := []grantOptionView{
		expectedFieldOption(bootstrapAssigned, "title", lifecycleField, false),
		expectedFieldOption(bootstrapAssigned, "subtitle", lifecycleOtherField, true),
		expectedFieldOption(bootstrapAny, "subtitle", lifecycleOtherField, true),
		{Operation: userReaderGrant().Operation, Scope: rolesInstallationScope, CanDelegate: true},
	}
	assertProjectedGrantOptions(t, out.Options, expected)
	assertGrantOptionWrites(t, x)
}

func assertProjectedGrantOptions(t *testing.T, found, expected []grantOptionView) {
	t.Helper()
	if len(found) != len(expected) {
		t.Fatalf("expected %d grant options, got %d", len(expected), len(found))
	}
	for _, actual := range found {
		match := slices.IndexFunc(expected, func(option grantOptionView) bool {
			return actual.Operation == option.Operation && actual.Scope == option.Scope &&
				actual.CanDelegate == option.CanDelegate && actual.TypeID == option.TypeID &&
				actual.TypeKey == option.TypeKey && actual.ObjectScope == option.ObjectScope &&
				slices.Equal(actual.Fields, option.Fields)
		})
		if match < 0 {
			t.Fatalf("out-of-envelope, merged or repeated grant option: %+v", actual)
		}
		expected = slices.Delete(expected, match, match+1)
	}
}

func seedGrantOptionFields(t *testing.T, x assigneeFixture) {
	t.Helper()
	db := x.http.state.db
	_, err := db.Exec(`INSERT INTO public.content_fields(type_id,field_id,key) VALUES
 ($1,$2,'title'),($1,$3,'subtitle'),($1,$4,'restricted'),($5,$6,'other_title')`,
		assigneeTypeID, lifecycleField, lifecycleOtherField, assigneeMissingID, assigneeOtherType, assigneeOtherType)
	requireNoError(t, err)
	_, err = db.Exec(`INSERT INTO public.grant_fields(grant_id,type_id,field_id)
 SELECT id,type_id,$2 FROM public.role_grants WHERE role_id=$1 AND operation='content.permissions.delegate'`,
		x.managerRole, lifecycleField)
	requireNoError(t, err)
	_, err = db.Exec(`WITH direct AS (
 INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope,can_delegate)
 VALUES(gen_random_uuid(),$1,'content.items.read','content',$2,'any',true) RETURNING id,type_id
 ) INSERT INTO public.grant_fields(grant_id,type_id,field_id) SELECT id,type_id,$3 FROM direct`,
		x.managerRole, assigneeTypeID, lifecycleOtherField)
	requireNoError(t, err)
	_, err = db.Exec(`INSERT INTO public.role_grants(id,role_id,operation,can_delegate) VALUES
 (gen_random_uuid(),$1,'roles.create',false), (gen_random_uuid(),$1,'users.read',true)`, x.managerRole)
	requireNoError(t, err)
}

func expectedFieldOption(scope, key, fieldID string, canDelegate bool) grantOptionView {
	return grantOptionView{
		Operation: bootstrapOpRead, Scope: rolesContentScope, CanDelegate: canDelegate,
		TypeID: assigneeTypeID, TypeKey: assigneeTypeKey, ObjectScope: scope,
		Fields: []grantOptionFieldView{{ID: fieldID, Key: key}},
	}
}

func assertGrantOptionWrites(t *testing.T, x assigneeFixture) {
	t.Helper()
	f := x.http
	proposed := map[string]any{
		"operation": bootstrapOpRead, "scope": rolesContentScope, "can_delegate": false,
		"type_id": assigneeTypeID, "object_scope": bootstrapAssigned, "field_ids": []string{lifecycleField, lifecycleOtherField},
	}
	body := func(name string) string {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"name": name, "grants": []any{proposed}})
		requireNoError(t, err)
		return string(raw)
	}
	f.roleHTTP(t, http.MethodPost, rolesHTTPPath, body("Combined choices"), x.actor, http.StatusForbidden)
	proposed["field_ids"] = []string{lifecycleField}
	f.roleHTTP(t, http.MethodPost, rolesHTTPPath, body("Narrow choice"), x.actor, http.StatusOK)
}
