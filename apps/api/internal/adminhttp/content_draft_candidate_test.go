package adminhttp

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

type fakeCandidateDraftEditor struct {
	fakeDraftEditor
	operation string
	input     draftCandidateInput
}

func (f *fakeCandidateDraftEditor) recordCandidate(req identity.AuthRequest, key, item, operation string, input draftCandidateInput) (content.DraftState, error) {
	f.calls++
	f.req, f.target = req, routeTarget{id: item, typeKey: key}
	f.operation, f.input = operation, input
	return f.state, f.err
}

func (f *fakeCandidateDraftEditor) MarkSharedReadyForType(_ context.Context, req identity.AuthRequest, key, item string, expected, schema int64) (content.DraftState, error) {
	return f.recordCandidate(req, key, item, readySharedOperation, draftCandidateInput{ExpectedVersion: expected, SchemaNo: schema})
}

func (f *fakeCandidateDraftEditor) MarkReadyForType(_ context.Context, req identity.AuthRequest, key, item, locale string, expected, schema int64) (content.DraftState, error) {
	return f.recordCandidate(req, key, item, readyLocaleOperation, draftCandidateInput{ExpectedVersion: expected, SchemaNo: schema, Locale: locale})
}

func (f *fakeCandidateDraftEditor) PublishForType(_ context.Context, req identity.AuthRequest, key, item string, expected, schema int64, options content.PublicationOptions) (content.DraftState, error) {
	return f.recordCandidate(req, key, item, publishOperation, draftCandidateInput{ExpectedVersion: expected, SchemaNo: schema, IntendedLocales: options.Locales, AllocateSlug: options.AllocateSlug})
}

func (f *fakeDraftEditor) MarkSharedReadyForType(context.Context, identity.AuthRequest, string, string, int64, int64) (content.DraftState, error) {
	panic("unexpected mark shared ready")
}

func (f *fakeDraftEditor) MarkReadyForType(context.Context, identity.AuthRequest, string, string, string, int64, int64) (content.DraftState, error) {
	panic("unexpected mark locale ready")
}

func (f *fakeDraftEditor) PublishForType(context.Context, identity.AuthRequest, string, string, int64, int64, content.PublicationOptions) (content.DraftState, error) {
	panic("unexpected publish")
}

const validPublishRequest = `{"expected_version":"2","schema_no":"1","intended_locales":["pl","en"]}`

func TestDraftCandidateRoutesDispatchExpectedInputs(t *testing.T) {
	for _, tc := range []struct {
		suffix, operation, body string
		input                   draftCandidateInput
	}{
		{"/ready-shared", "contentMarkSharedReady", minimumDraftBody, draftCandidateInput{ExpectedVersion: 2, SchemaNo: 1}},
		{"/ready", "contentMarkReady", `{"expected_version":"2","schema_no":"1","locale":"pl"}`, draftCandidateInput{ExpectedVersion: 2, SchemaNo: 1, Locale: "pl"}},
		{"/publish", publishOperation, validPublishRequest, draftCandidateInput{ExpectedVersion: 2, SchemaNo: 1, IntendedLocales: []string{"pl", "en"}}},
		{"/publish", publishOperation, `{"expected_version":"2","schema_no":"1","intended_locales":["pl"],"allocate_slug":true}`, draftCandidateInput{ExpectedVersion: 2, SchemaNo: 1, IntendedLocales: []string{"pl"}, AllocateSlug: true}},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			editor := &fakeCandidateDraftEditor{}
			editor.state = content.DraftState{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 3}
			server := newDraftTLSServer(t, editor)
			out := lifecycleResponse(t, server, roleRequest(t, server, http.MethodPost, draftTestPath+tc.suffix, tc.body, fullKind), http.StatusOK)
			if editor.calls != 1 || editor.operation != tc.operation || !reflect.DeepEqual(editor.input, tc.input) {
				t.Fatalf("unexpected draft command: %s %+v", editor.operation, editor.input)
			}
			if editor.target != (routeTarget{id: exampleUUID, typeKey: draftTestTypeKey}) || editor.req.Token != testToken || editor.req.CSRF == "" {
				t.Fatal("draft command lost target or session boundary")
			}
			if !matchesDraftReceipt(t, out) {
				t.Fatalf("unexpected draft receipt: %v", out)
			}
		})
	}
}

func TestDraftCandidateRejectsInvalidPublishInput(t *testing.T) {
	editor := &fakeCandidateDraftEditor{}
	server := newDraftTLSServer(t, editor)
	for _, body := range []string{
		minimumDraftBody,
		`{"expected_version":2,"schema_no":"1","intended_locales":["pl"]}`,
		`{"expected_version":"02","schema_no":"1","intended_locales":["pl"]}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":[]}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":["pl","pl"]}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":[null]}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":["pl"],"locale":"pl"}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":["pl"],"allocate_slug":"true"}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":["pl"],"allocate_slug":null}`,
		`{"expected_version":"2","schema_no":"1","intended_locales":["pl"],"intended_locales":["en"]}`,
		validPublishRequest + " {}",
	} {
		req := roleRequest(t, server, http.MethodPost, draftTestPath+"/publish", body, fullKind)
		lifecycleResponse(t, server, req, http.StatusBadRequest)
	}
	req := roleRequest(t, server, http.MethodPost, draftTestPath+"/publish", strings.Repeat(" ", maxCredentialBodyBytes+1), fullKind)
	lifecycleResponse(t, server, req, http.StatusRequestEntityTooLarge)
	if editor.calls != 0 {
		t.Fatal("invalid publish reached the editor")
	}
}

func TestDraftPublishPreservesConflictCodes(t *testing.T) {
	for _, code := range []string{"not_ready", "locale_unavailable", "relation_unavailable", "route_conflict"} {
		editor := &fakeCandidateDraftEditor{}
		editor.err = &content.Error{Kind: content.Conflict, Code: code}
		server := newDraftTLSServer(t, editor)
		req := roleRequest(t, server, http.MethodPost, draftTestPath+"/publish", validPublishRequest, fullKind)
		out := lifecycleResponse(t, server, req, http.StatusConflict)
		if out["code"] != code {
			t.Fatalf("publish conflict got %v, want %s", out, code)
		}
	}
}

func TestDraftPublishIncludesPublicationIDInReceipt(t *testing.T) {
	editor := &fakeCandidateDraftEditor{}
	editor.state = content.DraftState{ItemID: exampleUUID, TypeID: exampleUUID, SchemaNo: 1, Version: 3, PublicationID: exampleUUID}
	server := newDraftTLSServer(t, editor)
	req := roleRequest(t, server, http.MethodPost, draftTestPath+"/publish", validPublishRequest, fullKind)
	out := lifecycleResponse(t, server, req, http.StatusOK)
	if out["publication_id"] != exampleUUID {
		t.Fatalf("publication ID missing from receipt: %v", out)
	}
	delete(out, "publication_id")
	if !matchesDraftReceipt(t, out) {
		t.Fatalf("unexpected fields in publication receipt: %v", out)
	}
}

func TestDraftCandidateUsesPrivateRequestBoundary(t *testing.T) {
	editor := &fakeCandidateDraftEditor{}
	server := newDraftTLSServer(t, editor)
	path := draftTestPath + "/ready-shared"
	lifecycleResponse(t, server, roleRequest(t, server, http.MethodPatch, path, minimumDraftBody, fullKind), http.StatusMethodNotAllowed)
	lifecycleResponse(t, server, roleRequest(t, server, http.MethodPost, path, minimumDraftBody, loginKind), http.StatusUnauthorized)
	for _, header := range []string{originHeader, testCSRFHeader} {
		req := roleRequest(t, server, http.MethodPost, path, minimumDraftBody, fullKind)
		req.Header.Del(header)
		lifecycleResponse(t, server, req, http.StatusForbidden)
	}
	disabled := newAdminTLSServer(t, &fakeAuth{})
	lifecycleResponse(t, disabled, roleRequest(t, disabled, http.MethodPost, path, minimumDraftBody, fullKind), http.StatusNotFound)
	if editor.calls != 0 {
		t.Fatal("invalid request reached the draft editor")
	}
}
