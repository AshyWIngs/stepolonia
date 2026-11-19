package adminintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/platform"
)

const (
	managedCollectionsPath = "/api/v1/admin/content-types"
	eventCollectionPath    = managedCollectionsPath + "/event"
	eventBaseFields        = `{"key":"title","kind":"string","scope":"localized","visibility":"public","required":true,"config":{"min_length":1}},
 {"key":"slug","kind":"string","scope":"localized","visibility":"public","required":true,"config":{"slug":true,"max_length":200}}`
	eventExtraField = `{"key":"subtitle","kind":"string","scope":"localized","visibility":"private","required":false}`
	eventCreateBody = `{"route_namespace":"events","initial_access":true,"fields":[` + eventBaseFields + `]}`
)

func TestAdminGenericCollectionAvailabilityHTTPSIntegration(t *testing.T) {
	const (
		key          = "meeting_records"
		namespace    = "collection-meeting-records"
		name         = "Protokoły spotkań"
		path         = managedCollectionsPath + "/" + key
		availability = path + "/availability"
		disable      = `{"expected_version":"1","enabled":false}`
		enable       = `{"expected_version":"2","enabled":true}`
		creation     = `{"type_key":"` + key + `","display_name":"` + name + `","route_namespace":"` + namespace +
			`","initial_access":true,"fields":[` + eventBaseFields +
			`,{"key":"body","kind":"markdown","scope":"localized","visibility":"public","required":false}]}`
	)
	f := newAuthHTTPFixtureWithEditors(t, func(state authFixtureState, auth http.Handler) http.Handler {
		reader := content.NewPublicReader(openPublicationRuntimeLogin(t, state.db))
		return platform.HandlerWithAuth(nil, reader, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	}, true)
	admin := authFull(t, f.state)
	actor := authReq(admin)
	call := func(method, path, payload string, status int) string {
		t.Helper()
		return sendDraftRequest(t, f.server, method, actor.Token, draftHTTPRequest{path: path, body: payload, csrf: actor.CSRF, status: status})
	}
	catalog := func() string { return call(http.MethodGet, managedCollectionsPath+"/renderers", "", http.StatusOK) }
	if rows := catalog(); !strings.Contains(rows, `"type_key":"","route_namespace":"collection","available":true`) {
		t.Fatalf("generic template missing: %s", rows)
	}
	call(http.MethodPost, managedCollectionsPath, strings.Replace(creation, namespace, "events", 1), http.StatusBadRequest)
	var created struct {
		TypeID      string `json:"type_id"`
		DisplayName string `json:"display_name"`
		Enabled     bool   `json:"enabled"`
	}
	requireNoError(t, json.Unmarshal([]byte(call(http.MethodPost, managedCollectionsPath, creation, http.StatusCreated)), &created))
	if created.TypeID == "" || created.DisplayName != name || !created.Enabled {
		t.Fatal("generic collection was not persisted with its name and availability")
	}

	enablePolishContentLocale(t, f.state.db)
	publisher := publicationFixture{owner: f.state.db, server: f.server, typeID: created.TypeID,
		typeKey: key, namespace: namespace, author: actor, publisher: actor}
	item, published := publishGenericCollection(t, publisher, call)
	draftPath := draftHTTPPrefix + key + "/" + item

	sendDraftPatch(t, f.server, actor.Token, draftHTTPRequest{availability, disable, "", http.StatusForbidden})
	sendDraftPatch(t, f.server, "", draftHTTPRequest{availability, disable, actor.CSRF, http.StatusUnauthorized})
	authorRole := fixturePublicationRole(t, f.state.db, "Zespół spotkań")
	grantPublicationFields(t, f.state.db, authorRole, created.TypeID, "content.items.read", bootstrapAny)
	author := fixturePublicationAccount(t, f.state, admin, "meetings@example.test", authorRole)
	sendDraftPatch(t, f.server, author.Token, draftHTTPRequest{availability, disable, author.CSRF, http.StatusForbidden})
	expireSessionStepUp(t, f.state.db, actor.Token)
	assertPublicationProblem(t, call(http.MethodPatch, availability, disable, http.StatusForbidden), stepUpRequiredProblem)
	authRefreshStep(t, f.state.db, actor.Token)
	call(http.MethodPatch, availability, `{"expected_version":"1","enabled":"false"}`, http.StatusBadRequest)
	_, err := f.state.db.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_disable_audit
 CHECK(operation<>'content.collection_disabled' OR result<>'success')`)
	requireNoError(t, err)
	call(http.MethodPatch, availability, disable, http.StatusServiceUnavailable)
	call(http.MethodGet, draftPath, "", http.StatusOK)
	_, err = f.state.db.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_disable_audit`)
	requireNoError(t, err)
	call(http.MethodPatch, availability, disable, http.StatusOK)
	call(http.MethodPatch, availability, disable, http.StatusConflict)
	assertDisabledCollection(t, publisher, draftPath, name, published, call)

	call(http.MethodPatch, availability, enable, http.StatusOK)
	call(http.MethodGet, draftPath, "", http.StatusOK)
	if options := call(http.MethodGet, managedCollectionsPath, "", http.StatusOK); !strings.Contains(options, name) {
		t.Fatalf("restored collection missing from authoring catalog: %s", options)
	}
	var audited int
	requireNoError(t, f.state.db.QueryRow(`SELECT count(*) FROM public.security_audit WHERE target_id=$1
 AND operation IN ('content.collection_disabled','content.collection_enabled') AND result='success' AND request_id IS NOT NULL`, created.TypeID).Scan(&audited))
	if audited != 2 {
		t.Fatalf("availability changes recorded %d success audit events, want 2", audited)
	}
}

func publishGenericCollection(t *testing.T, publisher publicationFixture, call func(string, string, string, int) string) (string, string) {
	t.Helper()
	item := publisher.prepareDraft(t)
	if drafts := call(http.MethodGet, draftHTTPPrefix+publisher.typeKey, "", http.StatusOK); !strings.Contains(drafts, editorialTitle) {
		t.Fatalf("generic collection's draft title missing: %s", drafts)
	}
	publisher.command(t, publisher.author, item, sharedReadyCommand, sharedReadyBody, http.StatusOK)
	publisher.command(t, publisher.author, item, localeReadyCommand, localeReadyBody, http.StatusOK)
	publisher.command(t, publisher.publisher, item, publishCommand, publishBody, http.StatusOK)
	published := publisher.public(t, http.StatusOK)
	if !strings.Contains(published, "/pl/"+publisher.namespace+"/"+editorialSlug) {
		t.Fatalf("unexpected generic publication: %s", published)
	}
	return item, published
}

func assertDisabledCollection(t *testing.T, publisher publicationFixture, draftPath, name, published string, call func(string, string, string, int) string) {
	t.Helper()
	draftList := draftHTTPPrefix + publisher.typeKey
	call(http.MethodGet, draftList, "", http.StatusNotFound)
	call(http.MethodGet, draftPath, "", http.StatusNotFound)
	call(http.MethodPost, draftList, createDraftHTTPSBody, http.StatusNotFound)
	call(http.MethodPatch, draftPath, `{"expected_version":"5","schema_no":"1","shared":{}}`, http.StatusNotFound)
	if options := call(http.MethodGet, managedCollectionsPath, "", http.StatusOK); strings.Contains(options, publisher.typeKey) {
		t.Fatalf("disabled collection visible in authoring catalog: %s", options)
	}
	var managed struct {
		Collections []content.CollectionSummary `json:"collections"`
	}
	requireNoError(t, json.Unmarshal([]byte(call(http.MethodGet, managedCollectionsPath+"/renderers", "", http.StatusOK)), &managed))
	if !slices.ContainsFunc(managed.Collections, func(item content.CollectionSummary) bool {
		return item.TypeKey == publisher.typeKey && item.DisplayName == name && !item.Enabled
	}) {
		t.Fatal("disabled collection missing from registry")
	}
	if publisher.public(t, http.StatusOK) != published {
		t.Fatal("disabling authoring changed the public snapshot")
	}
}

func TestAdminCollectionInitialAccessHTTPSIntegration(t *testing.T) {
	f := newAuthHTTPFixtureWithEditors(t, func(_ authFixtureState, auth http.Handler) http.Handler {
		return platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	}, true)
	actor := authReq(authFull(t, f.state))
	call := func(method, path, payload string, status int) string {
		t.Helper()
		return sendDraftRequest(t, f.server, method, actor.Token, draftHTTPRequest{path: path, body: payload, csrf: actor.CSRF, status: status})
	}

	renderers := call(http.MethodGet, managedCollectionsPath+"/renderers", "", http.StatusOK)
	if !strings.Contains(renderers, `"type_key":"event","route_namespace":"events","available":true`) {
		t.Fatalf("expected Event renderer to be available: %s", renderers)
	}
	expireSessionStepUp(t, f.state.db, actor.Token)
	assertPublicationProblem(t, call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusForbidden), stepUpRequiredProblem)
	authRefreshStep(t, f.state.db, actor.Token)
	call(http.MethodPost, managedCollectionsPath, strings.Replace(eventCreateBody, `"route_namespace"`, `"type_id":"injected","route_namespace"`, 1), http.StatusBadRequest)
	verifyRegistryAuditRollback(t, f.state, func() {
		call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusServiceUnavailable)
	})

	created := call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusCreated)
	if !strings.Contains(created, `"type_key":"event"`) || !strings.Contains(created, `"schema_no":"1"`) {
		t.Fatalf("wrong collection receipt: %s", created)
	}
	typeID, roleID := assertInitialEventGrants(t, f.state)
	if !strings.Contains(created, fmt.Sprintf(`"applied_access":{"role_id":%q,"grants":[`, roleID)) {
		t.Fatalf("missing access receipt: %s", created)
	}
	call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusConflict)
	call(http.MethodPost, draftHTTPPrefix+"event", createDraftHTTPSBody, http.StatusCreated)
	const revision = `{"expected_version":"1","initialize_new_field_access":true,"fields":[` + eventBaseFields + `,` + eventExtraField + `]}`
	expireSessionStepUp(t, f.state.db, actor.Token)
	assertPublicationProblem(t, call(http.MethodPatch, eventCollectionPath, revision, http.StatusForbidden), stepUpRequiredProblem)
	authRefreshStep(t, f.state.db, actor.Token)
	call(http.MethodPatch, eventCollectionPath, revision, http.StatusOK)
	assertAddedFieldGrants(t, f.state, typeID, roleID)
	call(http.MethodPatch, eventCollectionPath, revision, http.StatusConflict)
	call(http.MethodPatch, eventCollectionPath, strings.Replace(revision, `"expected_version":"1"`, `"expected_version":"2"`, 1), http.StatusForbidden)
	expireSessionStepUp(t, f.state.db, actor.Token)
	assertUninitializedFieldAvailableToManagers(t, f.state, typeID, call)
	assertBoundRoleGuard(t, f.state, typeID, roleID)
}

func TestAdminCollectionIconHTTPSIntegration(t *testing.T) {
	const (
		firstIcon  = `<svg viewBox="0 0 24 24"><circle r="6"/></svg>`
		secondIcon = `<svg viewBox="0 0 24 24"><rect width="16" height="16"/></svg>`
		firstSVG   = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle r="6"></circle></svg>`
		secondSVG  = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="16" height="16"></rect></svg>`
		invalidSVG = `<svg viewBox="0 0 24 24"><text>Event</text></svg>`
	)
	f := newCollectionRegistryFixture(t)
	call := f.requests(t)
	admin, actor := f.admin, authReq(f.admin)
	creation := func(svg string) string {
		return fmt.Sprintf(`{%s,"icon_svg":%q}`, strings.Trim(eventCreateBody, "{}"), svg)
	}
	revision := func(version, icon string) string {
		return fmt.Sprintf(`{"expected_version":%q,"fields":[%s]%s}`, version, eventBaseFields, icon)
	}
	withIcon := func(svg string) string { return fmt.Sprintf(`,"icon_svg":%q`, svg) }

	assertPublicationProblem(t, call(http.MethodPost, managedCollectionsPath, creation(invalidSVG), http.StatusBadRequest), "invalid_collection_icon")
	assertCollectionIcon(t, call(http.MethodPost, managedCollectionsPath, creation(firstIcon), http.StatusCreated), firstSVG, "1")
	assertCollectionIcon(t, call(http.MethodGet, eventCollectionPath, "", http.StatusOK), firstSVG, "1")
	assertCollectionIconIndexes(t, call, firstSVG)

	limited := fixturePublicationAccount(t, f.state, admin, "reader@example.test",
		fixturePublicationRole(t, f.state.db, "Czytelnik wydarzeń"))
	// Metadata writes reuse the version guard and do not require fresh MFA.
	expireSessionStepUp(t, f.state.db, actor.Token)
	change := revision("1", withIcon(secondIcon))
	sendDraftPatch(t, f.server, actor.Token, draftHTTPRequest{eventCollectionPath, change, "", http.StatusForbidden})
	sendDraftPatch(t, f.server, limited.Token, draftHTTPRequest{eventCollectionPath, change, limited.CSRF, http.StatusForbidden})
	assertCollectionIcon(t, call(http.MethodPatch, eventCollectionPath, change, http.StatusOK), secondSVG, "2")
	call(http.MethodPatch, eventCollectionPath, change, http.StatusConflict)
	assertPublicationProblem(t, call(http.MethodPatch, eventCollectionPath, revision("2", withIcon(invalidSVG)), http.StatusBadRequest), "invalid_collection_icon")
	call(http.MethodPatch, eventCollectionPath, revision("2", `,"icon_svg":null`), http.StatusBadRequest)
	assertCollectionIcon(t, call(http.MethodGet, eventCollectionPath, "", http.StatusOK), secondSVG, "2")

	assertCollectionIcon(t, call(http.MethodPatch, eventCollectionPath, revision("2", ""), http.StatusOK), secondSVG, "3")
	assertCollectionIcon(t, call(http.MethodPatch, eventCollectionPath, revision("3", withIcon(" \t ")), http.StatusOK), "", "4")
	assertCollectionIcon(t, call(http.MethodGet, eventCollectionPath, "", http.StatusOK), "", "4")
	assertCollectionIconIndexes(t, call, "")
}

func assertCollectionIcon(t *testing.T, body, svg, version string) {
	t.Helper()
	var response struct {
		IconSVG  *string `json:"icon_svg"`
		Version  string  `json:"version"`
		SchemaNo string  `json:"schema_no"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &response))
	if response.IconSVG == nil || *response.IconSVG != svg || response.Version != version || response.SchemaNo != "1" {
		t.Fatalf("expected collection version %s, schema 1 and icon %q, got %s", version, svg, body)
	}
}

func TestAdminScopedCollectionIconHTTPSIntegration(t *testing.T) {
	const (
		iconPath = eventCollectionPath + "/icon"
		icon     = `<svg viewBox="0 0 24 24"><circle r="6"/></svg>`
		savedSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle r="6"></circle></svg>`
	)
	f := newCollectionRegistryFixture(t)
	call := f.requests(t)
	admin, actor := f.admin, authReq(f.admin)
	body := call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusCreated)
	var created struct {
		IconVersion string `json:"icon_version"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &created))
	payload := fmt.Sprintf(`{"expected_version":%q,"icon_svg":%q}`, created.IconVersion, icon)
	sendDraftRequest(t, f.server, http.MethodPost, actor.Token, draftHTTPRequest{iconPath, payload, "", http.StatusForbidden})
	limited := fixturePublicationAccount(t, f.state, admin, "observer@example.test",
		fixturePublicationRole(t, f.state.db, "Obserwator"))
	sendDraftRequest(t, f.server, http.MethodPost, limited.Token, draftHTTPRequest{iconPath, payload, limited.CSRF, http.StatusForbidden})
	expireSessionStepUp(t, f.state.db, actor.Token)
	call(http.MethodPatch, eventCollectionPath, `{"expected_version":"1","fields":[`+eventBaseFields+`,`+eventExtraField+`]}`, http.StatusOK)
	call(http.MethodPost, iconPath, payload, http.StatusOK)
	var current struct {
		Icon     string            `json:"icon_svg"`
		Version  string            `json:"version"`
		SchemaNo string            `json:"schema_no"`
		Fields   []json.RawMessage `json:"fields"`
	}
	body = call(http.MethodGet, eventCollectionPath, "", http.StatusOK)
	requireNoError(t, json.Unmarshal([]byte(body), &current))
	if current.Version != "3" || current.SchemaNo != "2" || current.Icon != savedSVG || len(current.Fields) != 3 {
		t.Fatal("scoped icon save did not preserve the new schema")
	}
	call(http.MethodPost, iconPath, payload, http.StatusConflict)
	assertCollectionIconIndexes(t, call, savedSVG)
}

func TestAdminScopedCollectionFieldsHTTPSIntegration(t *testing.T) {
	const path = eventCollectionPath + "/fields"
	const titleField = "title"
	f := newCollectionRegistryFixture(t)
	call := f.requests(t)
	created := call(http.MethodPost, managedCollectionsPath, eventCreateBody, http.StatusCreated)
	titleVersion := fieldLabelToken(t, created, titleField)
	labels := fmt.Sprintf(`"labels":[{"key":"title","expected_version":%q,"display_labels":{"pl":{"label":"Tytuł wydarzenia"}}}]`, titleVersion)
	add := `"add":[` + eventExtraField + `]`
	call(http.MethodPost, path, `{`+add+`,`+labels+`}`, http.StatusBadRequest)
	call(http.MethodPost, path, `{"initialize_new_field_access":true,`+labels+`}`, http.StatusBadRequest)
	added := call(http.MethodPost, path, `{"expected_schema_no":"1",`+add+`}`, http.StatusOK)
	if fieldLabelToken(t, added, titleField) != titleVersion {
		t.Fatal("adding a field changed the saved label version of an existing field")
	}
	changed := call(http.MethodPost, path, `{`+labels+`}`, http.StatusOK)
	var persisted struct {
		SchemaNo int64 `json:"schema_no,string"`
	}
	requireNoError(t, json.Unmarshal([]byte(changed), &persisted))
	if fieldLabelToken(t, changed, titleField) == titleVersion || persisted.SchemaNo != 2 {
		t.Fatal("changing a label did not advance only its label scope")
	}
	call(http.MethodPost, path, `{`+labels+`}`, http.StatusConflict)
	call(http.MethodPost, path, `{"expected_schema_no":"1",`+add+`}`, http.StatusConflict)
	expireSessionStepUp(t, f.state.db, f.admin.Token)
	const freshOnly = `{"expected_schema_no":"2","initialize_new_field_access":true,"add":[{"key":"location_note","kind":"string","scope":"localized","visibility":"private","required":false}]}`
	assertPublicationProblem(t, call(http.MethodPost, path, freshOnly, http.StatusForbidden), stepUpRequiredProblem)
}

type collectionRegistryFixture struct {
	authHTTPFixture
	admin identity.SessionTokens
}

func newCollectionRegistryFixture(t *testing.T) collectionRegistryFixture {
	t.Helper()
	f := newAuthHTTPFixtureWithEditors(t, func(_ authFixtureState, auth http.Handler) http.Handler {
		return platform.HandlerWithAuth(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), auth)
	}, true)
	return collectionRegistryFixture{authHTTPFixture: f, admin: authFull(t, f.state)}
}

func (f collectionRegistryFixture) requests(t *testing.T) func(string, string, string, int) string {
	t.Helper()
	actor := authReq(f.admin)
	return func(method, path, payload string, status int) string {
		t.Helper()
		return sendDraftRequest(t, f.server, method, actor.Token, draftHTTPRequest{path: path, body: payload, csrf: actor.CSRF, status: status})
	}
}

func fieldLabelToken(t *testing.T, body, key string) string {
	t.Helper()
	var collection struct {
		Fields []struct {
			Key     string `json:"key"`
			Version string `json:"label_version"`
		} `json:"fields"`
	}
	requireNoError(t, json.Unmarshal([]byte(body), &collection))
	for _, field := range collection.Fields {
		if field.Key == key && len(field.Version) == 64 {
			return field.Version
		}
	}
	t.Fatalf("missing scoped token for %q", key)
	return ""
}

func assertCollectionIconIndexes(t *testing.T, call func(string, string, string, int) string, svg string) {
	t.Helper()
	type collectionIcon struct {
		TypeKey string  `json:"type_key"`
		IconSVG *string `json:"icon_svg"`
	}
	var browse struct {
		Items []collectionIcon `json:"items"`
	}
	var manage struct {
		Collections []collectionIcon `json:"collections"`
	}
	requireNoError(t, json.Unmarshal([]byte(call(http.MethodGet, managedCollectionsPath, "", http.StatusOK)), &browse))
	requireNoError(t, json.Unmarshal([]byte(call(http.MethodGet, managedCollectionsPath+"/renderers", "", http.StatusOK)), &manage))
	for _, rows := range [][]collectionIcon{browse.Items, manage.Collections} {
		if !slices.ContainsFunc(rows, func(item collectionIcon) bool {
			return item.TypeKey == "event" && item.IconSVG != nil && *item.IconSVG == svg
		}) {
			t.Fatalf("collection icon %q missing from the Event summary", svg)
		}
	}
}

func assertUninitializedFieldAvailableToManagers(t *testing.T, state authFixtureState, typeID string, call func(string, string, string, int) string) {
	t.Helper()
	const extra = `{"key":"internal_note","kind":"text","scope":"localized","visibility":"private","required":false}`
	const revise = `{"expected_version":"2","fields":[` + eventBaseFields + `,` + eventExtraField + `,` + extra + `]}`
	revised := call(http.MethodPatch, eventCollectionPath, revise, http.StatusOK)
	var fieldID string
	var grants int
	requireNoError(t, state.db.QueryRow(`SELECT f.field_id::text,
	 (SELECT count(*) FROM public.grant_fields g WHERE g.field_id=f.field_id AND g.type_id=f.type_id)
	 FROM public.content_fields f WHERE f.type_id=$1 AND f.key='internal_note'`, typeID).Scan(&fieldID, &grants))
	read := call(http.MethodGet, eventCollectionPath, "", http.StatusOK)
	idJSON := fmt.Sprintf(`"field_id":%q`, fieldID)
	if grants != 0 || strings.Contains(revised, `"applied_access"`) || !strings.Contains(revised, idJSON) || !strings.Contains(read, idJSON) {
		t.Fatalf("uninitialized field: grants=%d, revised=%s, read=%s", grants, revised, read)
	}
}

func verifyRegistryAuditRollback(t *testing.T, state authFixtureState, create func()) {
	t.Helper()
	_, err := state.db.Exec(`ALTER TABLE public.security_audit ADD CONSTRAINT reject_initial_access_audit CHECK (operation<>'content.initial_access_granted')`)
	requireNoError(t, err)
	create()
	_, err = state.db.Exec(`ALTER TABLE public.security_audit DROP CONSTRAINT reject_initial_access_audit`)
	requireNoError(t, err)
	var count int
	requireNoError(t, state.db.QueryRow(`SELECT count(*) FROM public.content_types WHERE key='event'`).Scan(&count))
	if count != 0 {
		t.Fatal("collection creation survived failed Q25 audit")
	}
}

func assertInitialEventGrants(t *testing.T, state authFixtureState) (string, string) {
	t.Helper()
	var typeID, roleID string
	var grants, fieldGrants, memberships, events int
	requireNoError(t, state.db.QueryRow(`SELECT t.id::text,r.id::text,
 (SELECT count(*) FROM public.role_grants g WHERE g.role_id=r.id AND NOT g.can_delegate AND g.type_id=t.id),
 (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON g.id=f.grant_id WHERE g.role_id=r.id),
 (SELECT count(*) FROM public.user_roles m WHERE m.role_id=r.id AND m.user_id=$1),
 (SELECT count(*) FROM public.security_audit a WHERE a.actor_id=$1 AND a.target_id=t.id
  AND a.operation='content.initial_access_granted' AND a.details->>'reason'='explicit_initial_access' AND a.details->>'role_id'=r.id::text)
 FROM public.content_types t JOIN public.roles r ON r.self_access_type_id=t.id AND r.self_access_user_id=$1
 WHERE t.key='event' AND r.state='active'`, state.user).Scan(&typeID, &roleID, &grants, &fieldGrants, &memberships, &events))
	if grants != 6 || fieldGrants != 10 || memberships != 1 || events != 1 {
		t.Fatalf("invalid initial access: grants=%d fields=%d memberships=%d audit=%d", grants, fieldGrants, memberships, events)
	}
	return typeID, roleID
}

func assertAddedFieldGrants(t *testing.T, state authFixtureState, typeID, roleID string) {
	t.Helper()
	var exact, all, events int
	requireNoError(t, state.db.QueryRow(`SELECT
 (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON f.grant_id=g.id
  JOIN public.content_fields d ON d.type_id=f.type_id AND d.field_id=f.field_id
  WHERE g.role_id=$1 AND d.type_id=$2 AND d.key='subtitle' AND g.object_scope='any' AND NOT g.can_delegate
  AND g.operation IN ('content.items.read','content.items.update','content.items.ready','content.items.publish')),
 (SELECT count(*) FROM public.grant_fields f JOIN public.role_grants g ON f.grant_id=g.id WHERE g.role_id=$1),
 (SELECT count(*) FROM public.security_audit WHERE target_id=$2 AND operation='content.new_field_access_granted'
  AND details->>'role_id'=$1::text)`, roleID, typeID).Scan(&exact, &all, &events))
	if exact != 4 || all != 14 || events != 1 {
		t.Fatalf("invalid new field access: exact=%d all=%d audit=%d", exact, all, events)
	}
}

func assertBoundRoleGuard(t *testing.T, state authFixtureState, typeID, roleID string) {
	t.Helper()
	_, err := state.db.ExecContext(context.Background(), `INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope)
 VALUES(gen_random_uuid(),$1,'content.items.assign','content',$2,'any')`, roleID, typeID)
	if err == nil {
		t.Fatal("bound role allowed the assignment operation")
	}
	_, err = state.db.Exec(`UPDATE public.roles SET self_access_type_id=NULL,self_access_user_id=NULL WHERE id=$1`, roleID)
	if err == nil {
		t.Fatal("bound role identity was mutable")
	}
}
