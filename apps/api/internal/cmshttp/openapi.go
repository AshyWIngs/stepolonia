package cmshttp

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// OpenAPIJSON describes the registered private auth transport independently of
// the public Huma API and SDK. Generating it does not register HTTP handlers.
func OpenAPIJSON() ([]byte, error) {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	for name, schema := range privateSchemas() {
		registry.Map()[name] = schema
	}
	paths := map[string]*huma.PathItem{}
	for path, rt := range privateRoutes() {
		op := &huma.Operation{
			OperationID: rt.operationID, Summary: rt.summary,
			Description: rt.description + " " + boundaryDescription,
			Tags:        []string{authTag(rt)},
			Parameters:  authParameters(rt), Responses: authResponses(rt), Security: authSecurity(rt),
		}
		item := &huma.PathItem{}
		if rt.method == http.MethodGet {
			item.Get = op
		} else {
			name := rt.operationID + "Request"
			schema, example := credentialSchema(rt)
			registry.Map()[name] = schema
			op.RequestBody = &huma.RequestBody{
				Required:    true,
				Description: "One JSON object, at most 4096 bytes. Unknown or duplicate keys, null, incorrect scalar types and trailing JSON are rejected. All listed credential fields are required for a successful command. Missing strings decode as empty strings and fail service checks with the documented generic error; expected_version is a required positive integer. Examples are synthetic.",
				Content:     map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(name), Example: example}},
			}
			if rt.operationID == issueOnboardingOperation {
				op.Parameters = append(op.Parameters, &huma.Param{Name: "id", In: "path", Required: true,
					Description: "Pending user UUID; cannot select the actor or grant permissions.",
					Schema:      uuidSchema(syntheticUserID)})
			}
			item.Post = op
		}
		paths[path] = item
	}
	return json.Marshal(&huma.OpenAPI{
		OpenAPI: "3.1.0",
		Info:    &huma.Info{Title: "Stepolonia private authentication", Version: "1.0.0", Description: "Implemented private session and pending-user onboarding operations only. This private contract is excluded from the public TypeScript SDK. Examples are synthetic and never authenticate. Flow: GET csrf, POST login, POST mfa/verify; replace X-CSRF-Token after every successful token response. Recovery and invited-user enrollment are separate restricted flows; neither grants CMS permissions. " + boundaryDescription},
		Tags:    authTags(),
		Servers: []*huma.Server{{URL: "/", Description: "Current installation; authentication requires its canonical HTTPS origin."}},
		Paths:   paths, Components: &huma.Components{Schemas: registry, SecuritySchemes: authSecuritySchemes()},
	})
}

func authSecurity(rt route) []map[string][]string {
	security := []map[string][]string{}
	for _, kind := range sortedCookieKinds() {
		if rt.kind == "" || kind == rt.kind {
			security = append(security, map[string][]string{cookieNames[kind]: {}})
		}
	}
	if rt.method == http.MethodGet {
		security = append(security, map[string][]string{})
	}
	return security
}

func authSecuritySchemes() map[string]*huma.SecurityScheme {
	schemes := map[string]*huma.SecurityScheme{}
	for kind, name := range cookieNames {
		schemes[name] = &huma.SecurityScheme{Type: "apiKey", In: "cookie", Name: name,
			Description: "Browser-managed " + kind + " session; exactly one recognized session cookie is allowed. Secure, HttpOnly, SameSite=Strict, Path=/, no Domain. Swagger cannot set an HttpOnly cookie manually; use the same-origin HTTPS flow."}
	}
	return schemes
}

const boundaryDescription = "All requests require the configured canonical HTTPS authority, with TLS directly or HTTPS asserted by an explicitly trusted proxy. POST requires exactly one Origin matching that canonical origin, application/json (optionally charset=utf-8), exactly one 43-character X-CSRF-Token, and exactly one matching session cookie. GET csrf accepts that Origin or both Sec-Fetch-Site: same-origin and a same-origin HTTPS Referer. Query strings are rejected. There is no cross-origin CORS access. Responses are Cache-Control: no-store and X-Content-Type-Options: nosniff. Browser-managed Origin, Referer and cookies cannot be forged through Swagger UI; plain HTTP development cannot exercise authentication."

const schemaPrefix = "#/components/schemas/"

func schemaRef(name string) *huma.Schema {
	return &huma.Schema{Ref: schemaPrefix + name}
}

func stringSchema(description string, examples ...any) *huma.Schema {
	return &huma.Schema{Type: "string", Description: description, Examples: examples}
}

func credentialSchema(rt route) (*huma.Schema, map[string]any) {
	props := map[string]*huma.Schema{}
	example := map[string]any{}
	if rt.fields != "" {
		for _, field := range strings.Split(rt.fields, ",") {
			property := credentialProperty(rt, field)
			props[field], example[field] = property, property.Examples[0]
		}
	}
	var required []string
	if rt.fields != "" {
		required = strings.Split(rt.fields, ",")
	}
	return closedObjectSchema("Private command input accepted by "+rt.operationID+". All listed fields are necessary for a successful command. expected_version is required for token mutations; empty objects are intentional only for cancellation and logout.", props, required, example), example
}

func credentialProperty(rt route, field string) *huma.Schema {
	var property *huma.Schema
	switch field {
	case "login":
		property = stringSchema("Account login normalized by Unicode NFC and case folding. The normalized value must be nonempty, at most 256 UTF-8 bytes, without surrounding whitespace, NUL, CR or LF. Invalid or unknown accounts use the same credential failure response.", "docs-editor")
		property.MinLength = intPointer(1)
		property.Pattern = `^[^\x00\r\n]+$`
		if rt.operationID == createUserOperation {
			property.Description = "New account login normalized by Unicode NFC and case folding. Must be nonempty, at most 256 UTF-8 bytes, without surrounding whitespace, NUL, CR or LF. Invalid input returns 400; an existing normalized login returns 409."
		}
	case "token":
		property = stringSchema("One-time onboarding secret received over a verified external channel; expires after at most 24 hours and is consumed at enrollment start. Send only in this JSON body, never a URL or log.", syntheticOnboardingToken)
		property.MinLength, property.MaxLength = intPointer(43), intPointer(43)
		property.Pattern = "^[A-Za-z0-9_-]{43}$"
		property.WriteOnly = true
	case expectedVersionField:
		property = &huma.Schema{Type: "integer", Format: "int64", Description: "Required positive current user version; checked under the write mutex to reject stale issuance or reissue.", Minimum: floatPointer(1), Examples: []any{1}}
	case "password":
		property = stringSchema("Current account password; case-sensitive UTF-8, at most 512 bytes, and never returned. Synthetic example only. Empty or incorrect values do not authenticate.", "Synthetic example password 42!")
		property.WriteOnly = true
		property.MinLength = intPointer(1)
		if rt.operationID == startEnrollmentOperation {
			property.MinLength, property.MaxLength = intPointer(15), intPointer(128)
			property.Description = "New password for this invited account; case-sensitive UTF-8, 15 to 128 Unicode code points and at most 512 bytes. Held only as a pending password hash until TOTP confirmation; never returned."
		}
	case credentialCodeField:
		property = stringSchema("Fresh six-digit TOTP from the enrolled factor; leading zeros are significant. Used counters cannot be replayed.", "012345")
		property.MinLength, property.MaxLength = intPointer(6), intPointer(6)
		property.Pattern = "^[0-9]{6}$"
		switch rt.operationID {
		case recoverOperation:
			property = stringSchema("One unused recovery code, not a TOTP. The code is reserved until completion, cancellation or expiry.", "0123456789abcdef0123456789abcdef")
			property.MinLength = intPointer(1)
		case completeRecoveryOperation, completeEnrollmentOperation:
			property.Description = "Fresh six-digit TOTP generated from this session's pending seed; leading zeros are significant."
		}
		property.WriteOnly = true
	}
	return property
}

const (
	csrfResponseSchema  = "CSRFResponse"
	pendingUserSchema   = "PendingUser"
	recoveryCodesSchema = "RecoveryCodes"
)

const syntheticOnboardingToken = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"

const syntheticUserID = "11111111-1111-4111-8111-111111111111"

const syntheticCSRF = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func privateSchemas() map[string]*huma.Schema {
	const csrfField = "csrf_token"
	csrf := stringSchema("Current session CSRF token. Send it verbatim as X-CSRF-Token on the next POST; successful token responses replace the previous value.", syntheticCSRF)
	csrf.MinLength, csrf.MaxLength = intPointer(43), intPointer(43)
	csrf.Pattern = "^[A-Za-z0-9_-]{43}$"
	secret := stringSchema("Pending base32 TOTP seed for this restricted session only. Enroll in an authenticator, then confirm before expiry. Treat the real response as a secret; this example is synthetic.", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP")
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = strings.Repeat("0", 31) + strconv.Itoa(i)
	}
	code := stringSchema("New single-use recovery code. Store securely outside the browser; it is returned only after successful enrollment or factor replacement.", codes[0])
	code.Pattern = "^[0-9a-f]{32}$"
	userExample := pendingUserResponse{ID: syntheticUserID, Login: "invited-editor", State: "pending", Version: 1}
	userSchema := responseDTOSchema("Pending account without credentials, roles, session access or private security fields. Version is required for a subsequent token mutation.", userExample)
	tokenExample := onboardingTokenResponse{Token: syntheticOnboardingToken, ExpiresAt: time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC), User: userExample}
	tokenSchema := responseDTOSchema("One-time disclosure of an onboarding token after its authorized mutation and audit commit. Deliver through a verified external channel; never cache or log.", tokenExample)
	tokenSchema.Properties["user"] = schemaRef(pendingUserSchema)
	enrollmentSchema := closedObjectSchema("Restricted own enrollment with a ten-minute maximum lifetime, bounded by the original onboarding token expiry. The seed and pending password do not grant CMS access.",
		map[string]*huma.Schema{csrfField: csrf, "secret": secret}, []string{csrfField, "secret"}, recoveryEnrollmentResponse{CSRFToken: syntheticCSRF, Secret: secret.Examples[0].(string)})
	recoverySchema := *enrollmentSchema
	recoverySchema.Description = "Restricted pending recovery enrollment, not an authenticated full session. The real seed must not be logged or cached."

	return map[string]*huma.Schema{
		pendingUserSchema: userSchema,
		"OnboardingToken": tokenSchema,
		"Enrollment":      enrollmentSchema,
		csrfResponseSchema: closedObjectSchema(
			"Successful session transition or CSRF rotation. The opaque session bearer stays in a Secure HttpOnly cookie and is never returned in JSON.",
			map[string]*huma.Schema{csrfField: csrf}, []string{csrfField}, csrfResponse{CSRFToken: syntheticCSRF}),
		"RecoveryEnrollment": &recoverySchema,
		recoveryCodesSchema: closedObjectSchema(
			"One-time disclosure of ten recovery codes after successful factor confirmation. All browser session cookies are cleared.",
			map[string]*huma.Schema{"recovery_codes": {Type: "array", Description: "Ten new single-use secrets from the completed enrollment or factor replacement.", Items: code, MinItems: intPointer(10), MaxItems: intPointer(10), Examples: []any{codes}}},
			[]string{"recovery_codes"}, recoveryCodesResponse{RecoveryCodes: codes}),
		"AuthProblem": responseDTOSchema(
			"Private transport problem; uses status, code and title only, without account existence or secret details. It is not the public Huma error model.",
			authProblem{Status: 401, Code: authenticationCode, Title: "Unauthorized"}),
	}

}

func closedObjectSchema(description string, properties map[string]*huma.Schema, required []string, example any) *huma.Schema {
	return &huma.Schema{Type: "object", Description: description, Properties: properties, Required: required, AdditionalProperties: false, Examples: []any{example}}
}

func intPointer(value int) *int { return &value }

func floatPointer(value float64) *float64 { return &value }

func authParameters(rt route) []*huma.Param {
	params := []*huma.Param{{Name: originHeader, In: "header", Required: rt.method == http.MethodPost,
		Description: "Exactly the configured canonical HTTPS origin. Browser-controlled; GET csrf may instead use same-origin Fetch Metadata and Referer.",
		Schema:      stringSchema("Canonical installation origin without path.", "https://cms.example.test")}}
	if rt.method == http.MethodPost {
		schema := stringSchema("Session-bound anti-CSRF token.", syntheticCSRF)
		schema.MinLength, schema.MaxLength = intPointer(43), intPointer(43)
		params = append(params, &huma.Param{Name: "X-CSRF-Token", In: "header", Required: true,
			Description: "Latest token returned by csrf or a successful session transition; must match the accompanying session. The synthetic example is not a credential.", Schema: schema})
	}
	return params
}

func constantStringSchema(value string, examples ...any) *huma.Schema {
	schema := stringSchema("", examples...)
	schema.Const = value
	return schema
}

func authResponseHeaders() map[string]*huma.Param {
	return map[string]*huma.Param{
		"X-Request-ID":           {Description: "Server-generated correlation identifier; new onboarding commands use a UUID shared with their audit events. Incoming header values are never trusted.", Schema: stringSchema("Opaque server request correlation identifier.")},
		"Cache-Control":          {Description: "Authentication data must never be cached.", Schema: constantStringSchema("no-store")},
		"X-Content-Type-Options": {Description: "Disables content sniffing.", Schema: constantStringSchema("nosniff")},
	}
}

func sessionCookieHeader() *huma.Param {
	return &huma.Param{Description: "Clears all recognized session cookies. Token responses also set exactly one current cookie with Secure; HttpOnly; SameSite=Strict; Path=/ and an expiry, without Domain. Completion, cancellation and logout only clear cookies. Multiple Set-Cookie header lines are emitted; JSON never exposes the bearer.", Schema: stringSchema("Browser-managed session cookie updates; not a request Authorization header.")}
}

func authResponses(rt route) map[string]*huma.Response {
	responses := map[string]*huma.Response{}
	successHeaders := authResponseHeaders()
	if rt.operationID != createUserOperation && rt.operationID != issueOnboardingOperation {
		successHeaders["Set-Cookie"] = sessionCookieHeader()
	}
	if rt.response == "" {
		responses["204"] = &huma.Response{Description: authSuccessDescription(rt), Headers: successHeaders}
	} else {
		schema := privateSchemas()[rt.response]
		responses["200"] = &huma.Response{Description: authSuccessDescription(rt), Headers: successHeaders, Content: map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(rt.response), Example: schema.Examples[0]}}}
	}
	for status, codes := range authErrorCodes(rt) {
		responses[strconv.Itoa(status)] = authErrorResponse(rt, status, codes)
	}
	return responses
}

func authErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{400: {validationCode}, 401: {authenticationCode, "invalid_credentials"}, 403: {originCode, csrfCode}, 429: {"rate_limited"}, 503: {"security_unavailable"}}
	if rt.method == http.MethodGet {
		codes[401] = []string{authenticationCode}
		codes[403] = []string{originCode}
	} else {
		codes[405] = []string{"method_not_allowed"}
		codes[413] = []string{"body_too_large"}
	}
	if rt.operationID == createUserOperation || rt.operationID == issueOnboardingOperation {
		codes[401] = []string{authenticationCode}
		codes[403] = []string{originCode, csrfCode, "permission_denied", "step_up_required"}
		codes[409] = []string{"version_conflict", "invalid_transition"}
		if rt.operationID == issueOnboardingOperation {
			codes[404] = []string{notFoundCode}
		}
	}
	if rt.operationID == cancelRecoveryOperation || rt.operationID == logoutOperation {
		codes[401] = []string{authenticationCode}
		delete(codes, 429)
	}
	return codes
}

func authErrorResponse(rt route, status int, codes []string) *huma.Response {
	examples := map[string]*huma.Example{}
	for _, code := range codes {
		examples[code] = &huma.Example{Summary: code, Value: authProblem{Status: status, Code: code, Title: http.StatusText(status)}}
	}
	headers := authResponseHeaders()
	description := http.StatusText(status) + ": " + strings.Join(codes, ", ") + "."
	switch status {
	case 429:
		description += authRateDescription(rt)
		headers["Retry-After"] = &huma.Param{Description: "Fixed retry delay in seconds; obey it before retrying credential checks.", Schema: constantStringSchema("900", "900")}
	case 503:
		description += " Security dependencies or credential-check capacity unavailable; fails closed."
	case 401:
		if rt.operationID == logoutOperation {
			headers["Set-Cookie"] = sessionCookieHeader()
			description += " Expired credentials clear cookies only after the request boundary succeeds."
		}
	}
	schema := &huma.Schema{AllOf: []*huma.Schema{schemaRef("AuthProblem"), {
		Type: "object",
		Properties: map[string]*huma.Schema{
			"status": {Const: status},
			"code":   {Enum: stringValues(codes)},
			"title":  {Const: http.StatusText(status)},
		},
	}}}
	return &huma.Response{Description: description, Headers: headers, Content: map[string]*huma.MediaType{"application/problem+json": {Schema: schema, Examples: examples}}}
}

func authRateDescription(rt route) string {
	if rt.operationID == createUserOperation || rt.operationID == issueOnboardingOperation {
		return " Denied commands reserve rolling 15-minute audit budgets: 5 per operation and client address, 30 per client IP and 100 per installation. Rotating a forged bearer does not reset this budget. Successful commands do not consume denial capacity."
	}
	if rt.method == http.MethodGet {
		return " New preauth issuance permits 30 attempts per client IP and 100 per installation in 15 minutes; existing-session CSRF rotation does not consume issuance budget."
	}
	if rt.operationID == startEnrollmentOperation {
		return " Credential checks reserve rolling 15-minute budgets: 5 per onboarding token, 30 per client IP and 100 per installation. Success releases its reservation; failed or interrupted checks retain it."
	}
	return " Credential checks reserve rolling 15-minute budgets: 5 per account, 30 per client IP and 100 per installation. Success releases its reservation; failed or interrupted checks retain it."
}

func sortedCookieKinds() []string {
	kinds := make([]string, 0, len(cookieNames))
	for kind := range cookieNames {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

func privateRoutes() map[string]route {
	all := make(map[string]route, len(routes)+len(userRoutes))
	for path, rt := range routes {
		all[authPrefix+path] = rt
	}
	for path, rt := range userRoutes {
		all[path] = rt
	}
	return all
}

func responseDTOSchema(description string, example any) *huma.Schema {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(registry, reflect.TypeOf(example))
	schema.Description, schema.Examples = description, []any{example}
	return schema
}

func uuidSchema(example string) *huma.Schema {
	schema := stringSchema("Pending account UUID.", example)
	schema.Format = "uuid"
	return schema
}

func authTags() []*huma.Tag {
	return []*huma.Tag{
		{Name: "authentication-sessions", Description: "Session initiation, password and TOTP verification, CSRF rotation, strong authentication and logout."},
		{Name: "account-recovery", Description: "Restricted recovery with an unused recovery code and confirmation of a replacement TOTP factor."},
		{Name: "account-enrollment", Description: "Restricted invited-user password and TOTP enrollment without CMS access."},
		{Name: "users", Description: "Authorized pending-account creation and one-time onboarding token issuance."},
	}
}

func authTag(rt route) string {
	switch rt.operationID {
	case createUserOperation, issueOnboardingOperation:
		return "users"
	case recoverOperation, completeRecoveryOperation, cancelRecoveryOperation:
		return "account-recovery"
	case startEnrollmentOperation, completeEnrollmentOperation:
		return "account-enrollment"
	default:
		return "authentication-sessions"
	}
}

func authSuccessDescription(rt route) string {
	switch rt.operationID {
	case "authCSRF":
		return "Preauth session created or existing session CSRF rotated; use the returned token on the next POST."
	case "authLogin":
		return "Password accepted; restricted login session replaces preauth. Verify a fresh TOTP with the returned CSRF token."
	case "authVerifyMFA":
		return "TOTP accepted; a full session replaces the login challenge and the returned CSRF token replaces the previous token."
	case "authStepUp":
		return "Password and TOTP accepted; full session and CSRF rotate with five-minute strong-authentication freshness."
	case recoverOperation:
		return "Recovery code reserved; restricted recovery session and pending TOTP seed returned with a new CSRF token."
	case completeRecoveryOperation:
		return "Replacement TOTP committed and previous factors and sessions revoked; ten new recovery codes returned once, cookies cleared and a new login required."
	case cancelRecoveryOperation:
		return "Recovery reservation released without replacing factors; restricted recovery session revoked and browser cookies cleared. No response body."
	case startEnrollmentOperation:
		return "Onboarding token consumed; restricted enrollment session and pending TOTP seed returned with a new CSRF token."
	case completeEnrollmentOperation:
		return "Password and TOTP committed; ten recovery codes returned once and cookies cleared. Account remains disabled without roles or CMS access."
	case createUserOperation:
		return "Pending account and audit committed without credentials, roles or sessions; current version returned."
	case issueOnboardingOperation:
		return "Previous onboarding token and restricted enrollment revoked; new one-time token and updated user version returned after audit commit."
	default:
		return "Current session revoked and browser session cookies cleared; no response body."
	}
}

func stringValues(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
