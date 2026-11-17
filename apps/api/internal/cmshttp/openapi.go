package cmshttp

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

const (
	csrfResponseSchema = "CSRFResponse"
	logoutOperation    = "authLogout"
	schemaPrefix       = "#/components/schemas/"
	syntheticCSRF      = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

// OpenAPIJSON describes the registered private auth transport independently of
// the public Huma API and SDK. Generating it does not register HTTP handlers.
func OpenAPIJSON() ([]byte, error) {
	registry := huma.NewMapRegistry(schemaPrefix, huma.DefaultSchemaNamer)
	for name, schema := range privateSchemas() {
		registry.Map()[name] = schema
	}
	paths := map[string]*huma.PathItem{}
	for path, rt := range routes {
		op := &huma.Operation{
			OperationID: rt.operationID, Summary: rt.summary,
			Description: rt.description + " " + boundaryDescription,
			Tags:        []string{"Private authentication"},
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
				Description: "One JSON object, at most 4096 bytes. Unknown or duplicate keys, null, non-string values and trailing JSON are rejected. Omitted credential fields decode as empty strings and are checked by authentication; a successful request supplies the credentials shown in the example.",
				Content:     map[string]*huma.MediaType{jsonType: {Schema: schemaRef(name), Example: example}},
			}
			item.Post = op
		}
		paths[authPrefix+path] = item
	}
	return json.Marshal(&huma.OpenAPI{
		OpenAPI: "3.1.0",
		Info:    &huma.Info{Title: "Stepolonia private authentication", Version: "1.0.0", Description: "Implemented session operations only. This private contract is excluded from the public TypeScript SDK. Examples are synthetic and never authenticate. Flow: GET csrf, POST login, POST mfa/verify; replace X-CSRF-Token after every successful token response. Recovery is a separate restricted flow. " + boundaryDescription},
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
			var property *huma.Schema
			switch field {
			case loginKind:
				property = stringSchema("Account login normalized by Unicode NFC and case folding. The normalized value must be nonempty, at most 256 UTF-8 bytes, without surrounding whitespace, NUL, CR or LF. Invalid or unknown accounts use the same credential failure response.", "docs-editor")
			case "password":
				property = stringSchema("Current account password; case-sensitive UTF-8, at most 512 bytes, and never returned. Synthetic example only. Empty or incorrect values do not authenticate.", "Synthetic example password 42!")
				property.WriteOnly = true
			case codeField:
				property = stringSchema("Fresh six-digit TOTP from the enrolled factor; leading zeros are significant. Used counters cannot be replayed.", "012345")
				switch rt.operationID {
				case "authRecover":
					property = stringSchema("One unused recovery code, not a TOTP. The code is reserved until completion, cancellation or expiry.", "0123456789abcdef0123456789abcdef")
				case "authCompleteRecovery":
					property.Description = "Fresh six-digit TOTP generated from the pending replacement seed; leading zeros are significant."
				}
				property.WriteOnly = true
			}
			props[field], example[field] = property, property.Examples[0]
		}
	}
	return closedObjectSchema("Credentials accepted by "+rt.operationID+". Fields may be absent at the JSON boundary; absent values become empty strings and do not bypass service authentication. Empty objects are intentional for cancellation and logout.", props, nil, example), example
}

func privateSchemas() map[string]*huma.Schema {
	csrf := stringSchema("Current session CSRF token. Send it verbatim as X-CSRF-Token on the next POST; successful token responses replace the previous value.", syntheticCSRF)
	csrf.MinLength, csrf.MaxLength = intPointer(43), intPointer(43)
	csrf.Pattern = "^[A-Za-z0-9_-]{43}$"
	secret := stringSchema("Pending base32 TOTP seed for this recovery session only. Enroll in an authenticator, then confirm before expiry. Treat the real response as a secret; this example is synthetic.", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP")
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = strings.Repeat("0", 31) + strconv.Itoa(i)
	}
	code := stringSchema("New single-use recovery code. Store securely outside the browser; it is returned only at factor replacement.", codes[0])
	code.Pattern = "^[0-9a-f]{32}$"
	return map[string]*huma.Schema{
		csrfResponseSchema: closedObjectSchema(
			"Successful session transition or CSRF rotation. The opaque session bearer stays in a Secure HttpOnly cookie and is never returned in JSON.",
			map[string]*huma.Schema{csrfField: csrf}, []string{csrfField}, map[string]any{csrfField: syntheticCSRF}),
		"RecoveryEnrollment": closedObjectSchema(
			"Restricted pending enrollment, not an authenticated full session. The real seed must not be logged or cached.",
			map[string]*huma.Schema{csrfField: csrf, secretField: secret}, []string{csrfField, secretField}, map[string]any{csrfField: syntheticCSRF, secretField: secret.Examples[0]}),
		"RecoveryCodes": closedObjectSchema(
			"One-time disclosure of ten replacement recovery codes after successful factor confirmation. All browser session cookies are cleared.",
			map[string]*huma.Schema{recoveryCodesField: {Type: "array", Description: "Ten new single-use secrets; replaces the previous recovery-code set.", Items: code, MinItems: intPointer(10), MaxItems: intPointer(10), Examples: []any{codes}}},
			[]string{recoveryCodesField}, map[string]any{recoveryCodesField: codes}),
		"AuthProblem": closedObjectSchema(
			"Private transport problem; uses status, code and title only, without account existence or secret details. It is not the public Huma error model.",
			map[string]*huma.Schema{statusField: {Type: "integer", Description: "HTTP response status.", Examples: []any{401}}, codeField: stringSchema("Stable machine-readable failure reason; see each status response for actual codes.", authenticationCode), titleField: stringSchema("Standard HTTP status phrase.", "Unauthorized")},
			[]string{statusField, codeField, titleField}, map[string]any{statusField: 401, codeField: authenticationCode, titleField: "Unauthorized"}),
	}

}

func closedObjectSchema(description string, properties map[string]*huma.Schema, required []string, example any) *huma.Schema {
	return &huma.Schema{Type: "object", Description: description, Properties: properties, Required: required, AdditionalProperties: false, Examples: []any{example}}
}

func intPointer(value int) *int { return &value }

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
		"Cache-Control":          {Description: "Authentication data must never be cached.", Schema: constantStringSchema("no-store")},
		"X-Content-Type-Options": {Description: "Disables content sniffing.", Schema: constantStringSchema("nosniff")},
	}
}

func sessionCookieHeader() *huma.Param {
	return &huma.Param{Description: "Clears all four recognized session cookies. Token responses also set exactly one current cookie with Secure; HttpOnly; SameSite=Strict; Path=/ and an expiry, without Domain. Completion, cancellation and logout only clear cookies. Multiple Set-Cookie header lines are emitted; JSON never exposes the bearer.", Schema: stringSchema("Browser-managed session cookie updates; not a request Authorization header.")}
}

func authResponses(rt route) map[string]*huma.Response {
	responses := map[string]*huma.Response{}
	successHeaders := authResponseHeaders()
	successHeaders["Set-Cookie"] = sessionCookieHeader()
	if rt.response == "" {
		responses["204"] = &huma.Response{Description: "Session revoked and browser session cookies cleared; no response body.", Headers: successHeaders}
	} else {
		schema := privateSchemas()[rt.response]
		responses["200"] = &huma.Response{Description: "Transition committed; use the returned CSRF token when present and retain browser-managed cookie updates.", Headers: successHeaders, Content: map[string]*huma.MediaType{jsonType: {Schema: schemaRef(rt.response), Example: schema.Examples[0]}}}
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
	if rt.operationID == "authCancelRecovery" || rt.operationID == logoutOperation {
		codes[401] = []string{authenticationCode}
		delete(codes, 429)
	}
	return codes
}

func authErrorResponse(rt route, status int, codes []string) *huma.Response {
	examples := map[string]*huma.Example{}
	for _, code := range codes {
		examples[code] = &huma.Example{Summary: code, Value: map[string]any{statusField: status, codeField: code, titleField: http.StatusText(status)}}
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
	return &huma.Response{Description: description, Headers: headers, Content: map[string]*huma.MediaType{"application/problem+json": {Schema: schemaRef("AuthProblem"), Examples: examples}}}
}

func authRateDescription(rt route) string {
	if rt.method == http.MethodGet {
		return " New preauth issuance permits 30 attempts per client IP and 100 per installation in 15 minutes; existing-session CSRF rotation does not consume issuance budget."
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
