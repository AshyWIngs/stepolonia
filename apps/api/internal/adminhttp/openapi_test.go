package adminhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type privateDocument struct {
	Tags       []*huma.Tag
	Paths      map[string]map[string]privateOperation
	Components struct{ Schemas map[string]*huma.Schema }
}

type privateOperation struct {
	OperationID string `json:"operationId"`
	Tags        []string
	Description string
	Parameters  []*huma.Param
	Security    []map[string][]string
	RequestBody *struct{ Content map[string]privateMedia } `json:"requestBody"`
	Responses   map[string]struct {
		Content map[string]privateMedia
		Headers map[string]*huma.Param
	}
}

type privateMedia struct {
	Schema   *huma.Schema
	Example  any
	Examples map[string]struct{ Value any }
}

func readPrivateDocument(t *testing.T) (privateDocument, []byte) {
	t.Helper()
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc privateDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, raw
}

func TestPrivateOpenAPICoversDispatchRegistry(t *testing.T) {
	doc, raw := readPrivateDocument(t)
	repeated, err := OpenAPIJSON()
	if err != nil || !bytes.Equal(raw, repeated) {
		t.Fatal("private specification is not deterministic", err)
	}
	if len(doc.Paths) != len(privateRoutes()) {
		t.Fatal("documented paths differ from dispatch registry")
	}
	ids := map[string]bool{}
	for path, methods := range privateRoutes() {
		for _, rt := range methods {
			op := checkRegistryOperation(t, doc, path, rt)
			if ids[op.OperationID] {
				t.Fatalf("duplicate operation ID: %s", op.OperationID)
			}
			ids[op.OperationID] = true
		}
	}
	for _, forbidden := range []string{"EncryptionKey", "DATABASE_URL", "password_hash", "ciphertext", "token_digest"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("private implementation detail leaked: %s", forbidden)
		}
	}
}

func checkRegistryOperation(t *testing.T, doc privateDocument, path string, rt route) privateOperation {
	t.Helper()
	operations := doc.Paths[path]
	op, ok := operations[strings.ToLower(rt.method)]
	if !ok || len(operations) != len(privateRoutes()[path]) || op.OperationID == "" || len(op.Description) < 100 {
		t.Fatalf("missing or incomplete operation %s: %+v", path, op)
	}
	if rt.method == http.MethodGet {
		if op.RequestBody != nil {
			t.Fatal("csrf GET must not declare a body")
		}
		return op
	}
	if op.RequestBody == nil {
		t.Fatalf("missing request body %s", path)
	}
	switch {
	case isRegistryOperation(rt.operationID):
		checkManagedCollectionExample(t, op, rt)
	case rt.operationID == accountFactorOperation:
		checkFactorRequestExamples(t, doc, op, rt)
	case securityPolicyRequestSchema(rt.operationID) != nil:
		checkSecurityPolicyExample(t, op, rt.operationID)
	default:
		checkRegistryRequestExample(t, doc, op, rt)
	}
	return op
}

func checkSecurityPolicyExample(t *testing.T, op privateOperation, operationID string) {
	t.Helper()
	raw, err := json.Marshal(op.RequestBody.Content[jsonMediaType].Example)
	if err != nil {
		t.Fatal(err)
	}
	if operationID == replaceRatePolicyOperation {
		if !decodeRatePolicy(raw, new(ratePolicyInput)) {
			t.Fatal("documented attempt policy example rejected by transport")
		}
		return
	}
	if !decodeSessionPolicy(raw, new(sessionPolicyInput)) {
		t.Fatal("documented session policy example rejected by transport")
	}
}

func checkFactorRequestExamples(t *testing.T, doc privateDocument, op privateOperation, rt route) {
	t.Helper()
	media := op.RequestBody.Content[jsonMediaType]
	schema := doc.Components.Schemas[strings.TrimPrefix(media.Schema.Ref, schemaPrefix)]
	if schema == nil || len(schema.OneOf) != 2 {
		t.Fatal("replacement requires preparation and confirmation request variants")
	}
	for _, variant := range schema.OneOf {
		raw, err := json.Marshal(variant.Examples[0])
		if err != nil || variant.AdditionalProperties != false || !allowedObject(raw, rt.fields, false) {
			t.Fatal("replacement example rejected by its JSON boundary")
		}
	}
}

func checkManagedCollectionExample(t *testing.T, op privateOperation, rt route) {
	t.Helper()
	raw, err := json.Marshal(op.RequestBody.Content[jsonMediaType].Example)
	if rt.operationID == registryIconOperation {
		if err != nil || !decodeClosedCommand(raw, registryWriteContracts[rt.operationID], new(registryIconInput)) {
			t.Fatal("documented collection icon example rejected by transport")
		}
		return
	}
	if rt.operationID == registryFieldChangesOperation {
		if err != nil || !decodeRegistryFieldChanges(raw, new(registryFieldChangesInput)) {
			t.Fatal("documented collection field changes example rejected by transport")
		}
		return
	}
	value, ok := decodeUniqueJSON(raw)
	object, objectOK := value.(map[string]any)
	if err != nil || !ok || !objectOK || !validRegistryInput(object, rt.operationID) {
		t.Fatal("documented collection example rejected by transport")
	}
}

func checkRegistryRequestExample(t *testing.T, doc privateDocument, op privateOperation, rt route) {
	t.Helper()
	if authTag(rt) == settingsTag {
		checkSettingsRequestExample(t, op, rt)
		return
	}
	if authTag(rt) == rolesTag && rt.fields == "" {
		checkRoleRequestExample(t, rt, op.RequestBody.Content[jsonMediaType])
		return
	}
	if rt.operationID == createDraftOperation {
		raw, err := json.Marshal(op.RequestBody.Content[jsonMediaType].Example)
		if _, ok := parseCreateDraftInput(raw); err != nil || !ok {
			t.Fatal("documented create draft example rejected by transport")
		}
		return
	}
	if rt.operationID == saveDraftOperation {
		raw, err := json.Marshal(op.RequestBody.Content[jsonMediaType].Example)
		var input saveDraftInput
		if err != nil || !validDraftInput(raw, &input) {
			t.Fatal("documented draft example rejected by transport")
		}
		return
	}
	if _, ok := draftCandidateContracts[rt.operationID]; ok {
		raw, err := json.Marshal(op.RequestBody.Content[jsonMediaType].Example)
		if _, ok := parseDraftCandidateInput(raw, rt.operationID); err != nil || !ok {
			t.Fatal("documented draft command example rejected by transport")
		}
		return
	}
	checkCredentialProperties(t, doc, rt, op.RequestBody.Content[jsonMediaType])
}

func checkRoleRequestExample(t *testing.T, rt route, body privateMedia) {
	t.Helper()
	raw, err := json.Marshal(body.Example)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(rt.method, rolesPath, bytes.NewReader(raw))
	req.Header.Set(contentTypeHeader, jsonMediaType)
	if _, code, _ := readRoleInput(httptest.NewRecorder(), req, rt.operationID); code != "" {
		t.Fatalf("documented role example rejected by transport: %s", rt.operationID)
	}
}

func checkCredentialProperties(t *testing.T, doc privateDocument, rt route, body privateMedia) {
	t.Helper()
	schema := doc.Components.Schemas[strings.TrimPrefix(body.Schema.Ref, schemaPrefix)]
	if schema == nil || schema.AdditionalProperties != false {
		t.Fatalf("missing closed credential schema %s", rt.operationID)
	}
	exampleJSON, err := json.Marshal(body.Example)
	if err != nil || !allowedObject(exampleJSON, rt.fields, rt.operationID == replaceContentAssigneesOperation) {
		t.Fatalf("example rejected by actual JSON boundary: %s", rt.operationID)
	}
	example, ok := body.Example.(map[string]any)
	if !ok {
		t.Fatal("credential example is not an object")
	}
	fields := []string{}
	if rt.fields != "" {
		fields = strings.Split(rt.fields, ",")
	}
	for _, field := range fields {
		property := schema.Properties[field]
		if property == nil || len(property.Description) < 30 || example[field] == "" || example[field] == nil {
			t.Fatalf("missing credential description/example for %s %s", rt.operationID, field)
		}
	}
	if len(schema.Properties) != len(fields) || len(example) != len(fields) {
		t.Fatalf("credential fields drifted from dispatch registry: %s", rt.operationID)
	}
}

func TestPrivateOpenAPIExamplesValidateAgainstSchemas(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	for name, schema := range doc.Components.Schemas {
		schema.PrecomputeMessages()
		registry.Map()[name] = schema
	}
	for name, schema := range doc.Components.Schemas {
		if len(schema.Description) < 40 || len(schema.Examples) == 0 {
			t.Fatalf("schema lacks meaningful metadata: %s", name)
		}
		for _, example := range schema.Examples {
			validatePrivateExample(t, registry, name, example)
		}
	}
	for path, methods := range doc.Paths {
		for _, op := range methods {
			checkResponseExamples(t, registry, path, op)
		}
	}
}

func TestPrivateDraftSummaryPresentationIsOptional(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	schema := doc.Components.Schemas[draftSummarySchemaName]
	schema.PrecomputeMessages()
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	var summary map[string]any
	if err := json.Unmarshal([]byte(`{"item_id":"`+exampleUUID+`","type_id":"`+exampleUUID+`","schema_no":"1","version":"2"}`), &summary); err != nil {
		t.Fatal(err)
	}
	validatePrivateValue(t, registry, schema, draftSummarySchemaName, summary)
	summary["titles"] = map[string]any{"pl": strings.Repeat("ą", 257)}
	privateSchemaRejects(t, registry, schema, summary)
	summary["titles"] = map[string]any{"pl": "Spacer po Ałmaty"}
	summary["published_locales"] = []any{"pl", "pl"}
	privateSchemaRejects(t, registry, schema, summary)
}

func validatePrivateExample(t *testing.T, registry huma.Registry, name string, value any) {
	t.Helper()
	schema := registry.Map()[name]
	if schema == nil {
		t.Fatalf("missing schema %s", name)
	}
	validatePrivateValue(t, registry, schema, name, value)
}

func validatePrivateValue(t *testing.T, registry huma.Registry, schema *huma.Schema, name string, value any) {
	t.Helper()
	var result huma.ValidateResult
	huma.Validate(registry, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	if len(result.Errors) != 0 {
		t.Fatalf("%s example invalid: %v", name, result.Errors)
	}
}

func checkResponseExamples(t *testing.T, registry huma.Registry, path string, op privateOperation) {
	t.Helper()
	for status, response := range op.Responses {
		if status == "204" {
			if len(response.Content) != 0 {
				t.Fatalf("204 declares content: %s", path)
			}
			continue
		}
		for _, media := range response.Content {
			checkMediaExamples(t, registry, path, status, media)
		}
	}
}

func checkMediaExamples(t *testing.T, registry huma.Registry, path, status string, media privateMedia) {
	t.Helper()
	name := strings.TrimPrefix(media.Schema.Ref, schemaPrefix)
	if media.Example == nil && len(media.Examples) == 0 {
		t.Fatalf("missing %s %s example", path, status)
	}
	if media.Example != nil {
		validatePrivateValue(t, registry, media.Schema, name, media.Example)
	}
	for _, example := range media.Examples {
		validatePrivateValue(t, registry, media.Schema, name, example.Value)
	}
}

func TestCSRFRegistryPreservesMethodBoundary(t *testing.T) {
	service := &fakeAuth{}
	server := newAdminTLSServer(t, service)
	response := requestAuth(t, server, authRequestOptions{method: http.MethodPost, path: "/csrf", body: "{}", origin: server.URL, media: jsonMediaType, csrf: testToken})
	if response.StatusCode != http.StatusNotFound || service.calls != 0 {
		t.Fatal("POST csrf changed transport behavior", response.StatusCode)
	}
	// Documentation generation must not require a service or mutate routing.
	if _, err := OpenAPIJSON(); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Config{CanonicalOrigin: server.URL}, service)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/unknown", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || service.calls != 0 {
		t.Fatal("documentation changed handler boundary")
	}
}

func TestPrivateOnboardingOpenAPIDenialBudget(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/users/{id}/onboarding-token"} {
		response, ok := doc.Paths[path]["post"].Responses["429"]
		if !ok {
			t.Fatalf("missing denial-budget status for %s", path)
		}
		retry := response.Headers["Retry-After"]
		if retry == nil || retry.Schema == nil || retry.Schema.Type != huma.TypeString || retry.Schema.Pattern != `^[1-9][0-9]*$` || retry.Schema.Const != nil {
			t.Fatalf("missing denial-budget retry contract for %s", path)
		}
		example := response.Content["application/problem+json"].Examples["rate_limited"].Value
		problem, ok := example.(map[string]any)
		if !ok || problem["status"] != float64(429) || problem["code"] != "rate_limited" {
			t.Fatalf("incorrect denial-budget example for %s", path)
		}
	}
}

func TestPrivateDocumentationGroupsOperations(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	expected := map[string][]string{
		"authentication-sessions": {"authCSRF", "authLogin", "authVerifyMFA", "authStepUp", "authLogout"},
		"account-recovery":        {"authRecover", "authCompleteRecovery", "authCancelRecovery"},
		"account-enrollment":      {"authStartEnrollment", "authCompleteEnrollment"},
		"users":                   {"usersCreate", "usersIssueOnboardingToken", "usersList", "usersGet", "usersActivate", "usersDisable", "usersReadRoleMemberships", "usersReplaceRoles"},
		expectedRolesTag:          {"rolesList", "rolesGet", "rolesReadGrantOptions", "rolesCreate", "rolesEdit", "rolesApplyChanges", "rolesCopy", "rolesDelete"},
		"portal-settings":         {"settingsReadEditable", "settingsApply", "settingsApplyAppearance", "settingsApplyLanguageFlag", "settingsApplyDetails", "settingsApplyLocaleDefaults", "settingsApplySlugRules", "settingsApplyCatalog"},
		"security-policy":         {"readSessionPolicy", "replaceSessionPolicy", "readRatePolicies", "replaceRatePolicy"},
		"content-assignments":     {"contentReadAssignees", "contentReplaceAssignees"},
		"content-drafts":          {"contentCreateDraft", "contentListDrafts", "contentListReadableTypes", "contentGetDraftSchema", "contentGetDraft", "contentSaveDraft", "contentMarkSharedReady", "contentMarkReady", "contentPublish", "contentAdoptDraftSchema", "contentUnpublish"},
		"account-security":        {"accountReadProfile", "accountChangePassword", "accountReplaceFactor", "accountRegenerateRecoveryCodes", "accountListSessions", "accountListTargetSessions", "accountRevokeSession", "accountRevokeAllSessions", "listSecurityAudit"},
		"content-registry":        {"registryListRenderers", "registryReadCollection", "registryCreateCollection", "registryReviseCollection", "registrySetCollectionAvailability", "registrySaveCollectionIcon", "registrySaveCollectionFields"},
	}
	for _, tc := range roleStateCases {
		expected[expectedRolesTag] = append(expected[expectedRolesTag], tc.operation)
	}
	declared := map[string]bool{}
	for _, tag := range doc.Tags {
		if _, ok := expected[tag.Name]; !ok || tag.Description == "" || declared[tag.Name] || !regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`).MatchString(tag.Name) {
			t.Fatalf("invalid root tag: %+v", tag)
		}
		declared[tag.Name] = true
	}
	if len(declared) != len(expected) {
		t.Fatal("missing domain tags")
	}
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if len(op.Tags) != 1 || !declared[op.Tags[0]] || !slices.Contains(expected[op.Tags[0]], op.OperationID) {
				t.Fatalf("incorrect operation group: %s %v", op.OperationID, op.Tags)
			}
		}
	}
}

func privateRegistry(doc privateDocument) huma.Registry {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	for name, schema := range doc.Components.Schemas {
		schema.PrecomputeMessages()
		registry.Map()[name] = schema
	}
	return registry
}

func privateSchemaRejects(t *testing.T, registry huma.Registry, schema *huma.Schema, value any) {
	t.Helper()
	var result huma.ValidateResult
	huma.Validate(registry, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	if len(result.Errors) == 0 {
		t.Fatalf("invalid payload accepted by contract: %#v", value)
	}
}

func TestPrivateRequiredCredentials(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	registry := privateRegistry(doc)
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if op.RequestBody == nil {
				continue
			}
			t.Run(op.OperationID, func(t *testing.T) { checkRequiredCredentials(t, registry, op) })
		}
	}
}

func checkRequiredCredentials(t *testing.T, registry huma.Registry, op privateOperation) {
	t.Helper()
	media := op.RequestBody.Content[jsonMediaType]
	schema := registry.SchemaFromRef(media.Schema.Ref)
	switch op.OperationID {
	case replaceRatePolicyOperation:
		checkRatePolicyRequiredFields(t, registry, schema, media.Example.(map[string]any))
		return
	case accountFactorOperation:
		validatePrivateValue(t, registry, schema, op.OperationID, map[string]any{})
		privateSchemaRejects(t, registry, schema, map[string]any{"replacement_token": strings.Repeat("A", 80)})
		return
	case "authLogout", "authCancelRecovery", accountCodesOperation, revokeSessionOperation, revokeAllSessionsOperation:
		validatePrivateValue(t, registry, schema, op.OperationID, map[string]any{})
		return
	}
	privateSchemaRejects(t, registry, schema, map[string]any{})
	example := media.Example.(map[string]any)
	for field := range schema.Properties {
		if optionalRequestField(op.OperationID, field) {
			if op.OperationID != registryFieldChangesOperation && op.OperationID != catalogSettingsOperation {
				validatePrivateValue(t, registry, schema, op.OperationID, copyWithoutField(example, field))
			}
			continue
		}
		if op.OperationID == saveDraftOperation && !slices.Contains(schema.Required, field) {
			continue
		}
		if !slices.Contains(schema.Required, field) {
			t.Fatalf("missing required field %s", field)
		}
		missing := copyWithoutField(example, field)
		privateSchemaRejects(t, registry, schema, missing)
		checkInvalidFieldValues(t, registry, schema, op.OperationID, field, missing)
	}
}

func checkRatePolicyRequiredFields(t *testing.T, registry huma.Registry, schema *huma.Schema, example map[string]any) {
	t.Helper()
	for field := range schema.Properties {
		privateSchemaRejects(t, registry, schema, copyWithoutField(example, field))
	}
	preauth := copyWithoutField(copyWithoutField(example, rateSubjectField), rateHashJobsField)
	preauth["scope"] = "preauth"
	validatePrivateValue(t, registry, schema, replaceRatePolicyOperation, preauth)
	preauth[rateSubjectField] = 1
	privateSchemaRejects(t, registry, schema, preauth)
}

func checkInvalidFieldValues(t *testing.T, registry huma.Registry, schema *huma.Schema, operation, field string, payload map[string]any) {
	t.Helper()
	for _, invalid := range []any{nil, false, ""} {
		if invalid == "" && allowsEmptySVG(operation, field) {
			continue
		}
		if invalid == false && schema.Properties[field].Type == huma.TypeBoolean {
			continue
		}
		payload[field] = invalid
		privateSchemaRejects(t, registry, schema, payload)
	}
}

func allowsEmptySVG(operation, field string) bool {
	return operation == languageFlagSettingsOperation && field == "svg" ||
		operation == registryIconOperation && field == collectionIconField
}

func optionalRequestField(operation, field string) bool {
	switch operation {
	case registryCreateOperation:
		return field == "initial_access" || field == typeKeyField || field == registryDisplayNameField || field == collectionIconField
	case registryReviseOperation:
		return field == "initialize_new_field_access" || field == collectionIconField
	case registryFieldChangesOperation:
		return true
	case applySettingsOperation:
		return field == slugRulesField || field == languageFlagsField
	case slugRulesSettingsOperation:
		return field != expectedVersionField
	case catalogSettingsOperation:
		return field == catalogMessagesField || field == catalogRemoveField
	case changeRoleOperation:
		return field != expectedVersionField
	default:
		return false
	}
}

func copyWithoutField(example map[string]any, omitted string) map[string]any {
	result := map[string]any{}
	for key, value := range example {
		if key != omitted {
			result[key] = value
		}
	}
	return result
}

func TestPrivateCredentialConstraints(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	registry := privateRegistry(doc)
	for field, cases := range map[string][]struct {
		path           string
		valid, invalid []any
	}{
		"code": {
			{"/mfa/verify", []any{"000001"}, []any{"12345", "1234567", "１２３４５６", "abcdef"}},
			{"/recovery", []any{"0123456789abcdef0123456789abcdef"}, []any{""}},
		},
		"token":    {{"/enrollment", []any{syntheticOnboardingToken}, []any{strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("!", 43)}}},
		"password": {{"/enrollment", []any{strings.Repeat("界", 15), strings.Repeat("🔐", 128)}, []any{strings.Repeat("a", 14), strings.Repeat("a", 129)}}},
	} {
		for _, tc := range cases {
			op := doc.Paths[authPrefix+tc.path]["post"]
			schema := registry.SchemaFromRef(op.RequestBody.Content[jsonMediaType].Schema.Ref).Properties[field]
			for _, valid := range tc.valid {
				validatePrivateValue(t, registry, schema, tc.path+"/"+field, valid)
			}
			for _, invalid := range tc.invalid {
				privateSchemaRejects(t, registry, schema, invalid)
			}
		}
	}
	schema := registry.SchemaFromRef(doc.Paths[onboardingTokenPath]["post"].RequestBody.Content[jsonMediaType].Schema.Ref)
	for _, invalid := range []any{0, -1, 1.5} {
		privateSchemaRejects(t, registry, schema, map[string]any{"expected_version": invalid})
	}
}

func TestPrivateErrorCodesConstrainEachStatus(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	registry := privateRegistry(doc)
	for path, methods := range doc.Paths {
		for _, op := range methods {
			for status, response := range op.Responses {
				media, ok := response.Content["application/problem+json"]
				if !ok {
					continue
				}
				t.Run(path+"/"+status, func(t *testing.T) { checkErrorCodeConstraints(t, registry, media) })
			}
		}
	}
}

func checkErrorCodeConstraints(t *testing.T, registry huma.Registry, media privateMedia) {
	t.Helper()
	if len(media.Schema.AllOf) != 2 || len(media.Schema.AllOf[1].Properties["code"].Enum) == 0 {
		t.Fatal("missing machine-readable error codes")
	}
	for _, example := range media.Examples {
		value := example.Value.(map[string]any)
		validatePrivateValue(t, registry, media.Schema, "problem response", value)
		wrongCode := copyWithoutField(value, "code")
		wrongCode["code"] = "unsupported_failure"
		privateSchemaRejects(t, registry, media.Schema, wrongCode)
		wrongStatus := copyWithoutField(value, "status")
		wrongStatus["status"] = 999
		privateSchemaRejects(t, registry, media.Schema, wrongStatus)
	}
}

func TestPrivateSessionErrorCodesPreserveGenericFailures(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	for _, path := range []string{authPrefix + "/csrf", authPrefix + "/logout", authPrefix + "/recovery/cancel"} {
		for _, op := range doc.Paths[path] {
			media := op.Responses["401"].Content["application/problem+json"]
			codes := media.Schema.AllOf[1].Properties["code"].Enum
			if len(codes) != 1 || codes[0] != "authentication_required" {
				t.Fatalf("incorrect session error contract: %s %v", path, codes)
			}
		}
	}
}

func TestPrivateLifecycleContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	if len(doc.Paths[usersPath]) != 2 {
		t.Fatal("list replaced account creation")
	}
	for _, path := range []string{usersPath, userPath} {
		op := doc.Paths[path]["get"]
		if len(op.Security) != 1 || op.Security[0]["__Host-sp_session"] == nil {
			t.Fatal("read authentication contract permits anonymous or restricted access")
		}
		if _, exists := op.Responses["200"].Headers["Set-Cookie"]; exists {
			t.Fatal("safe read promises session mutation")
		}
		if _, exists := op.Responses["429"]; exists {
			t.Fatal("safe read promises a denial budget it does not consume")
		}
		for _, parameter := range op.Parameters {
			if parameter.Name == testCSRFHeader {
				t.Fatal("read unexpectedly requires CSRF")
			}
		}
	}
}

func TestPrivateLifecycleProjectionAndPaginationContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	list := doc.Paths[usersPath]["get"]
	queries := map[string]*huma.Param{}
	for _, parameter := range list.Parameters {
		if parameter.In == "query" {
			queries[parameter.Name] = parameter
		}
	}
	if len(queries) != 2 || queries["limit"].Schema.Default != float64(20) || *queries["limit"].Schema.Maximum != 100 || *queries["offset"].Schema.Maximum != 100 {
		t.Fatal("missing bounded pagination contract")
	}
	safeState := doc.Components.Schemas[safeUserSchema].Properties["state"].Enum
	pendingState := doc.Components.Schemas[pendingUserSchema].Properties["state"].Enum
	if len(safeState) != 3 || !slices.Contains(safeState, any("active")) || len(pendingState) != 1 || pendingState[0] != "pending" {
		t.Fatal("safe lifecycle projection changed pending-only onboarding contract")
	}
}

func TestPrivateLifecycleMutationContract(t *testing.T) {
	doc, _ := readPrivateDocument(t)
	for _, path := range []string{activateUserPath, disableUserPath} {
		op := doc.Paths[path]["post"]
		codes := op.Responses["409"].Content["application/problem+json"].Schema.AllOf[1].Properties["code"].Enum
		if !slices.Contains(codes, any("last_admin_required")) || !slices.Contains(codes, any("version_conflict")) {
			t.Fatal("lifecycle conflicts missing from machine-readable contract")
		}
		if _, exists := op.Responses["200"].Headers["Set-Cookie"]; exists {
			t.Fatal("lifecycle promises session mutation")
		}
	}
}
