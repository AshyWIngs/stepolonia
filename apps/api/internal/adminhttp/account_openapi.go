package adminhttp

import (
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

const accountTag = "account-security"

func isAccountOperation(id string) bool {
	for _, route := range accountRoutes {
		if id == route.operationID {
			return true
		}
	}
	return false
}

func addAccountSchemas(schemas map[string]*huma.Schema) {
	const sessionItem = "AccountSession"
	const auditItem = "SecurityAuditEntry"
	now := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	session := accountSessionResponse{ID: exampleUUID, CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(12 * time.Hour), Current: true}
	event := auditEntryResponse{ID: exampleUUID, OccurredAt: now, Operation: "auth.login_completed", Result: "success"}
	schemas[accountProfileSchema] = responseDTOSchema("Signed-in account and its confirmed factor status.", accountProfileResponse{ID: exampleUUID, Login: "editor", MFAEnabled: true, RecoveryCodesRemaining: 10})
	schemas[sessionItem] = responseDTOSchema("Active full session metadata. Does not contain bearer or CSRF credentials.", session)
	schemas[auditItem] = responseDTOSchema("Safe audit record with a timestamp and optional identifiers.", event)
	schemas[accountSessionsSchema] = accountListSchema("Active full sessions of the signed-in account.", sessionItem, accountPageResponse[accountSessionResponse]{Items: []accountSessionResponse{session}})
	schemas[auditListSchema] = accountListSchema("Bounded page of security events in newest-first order.", auditItem, accountPageResponse[auditEntryResponse]{Items: []auditEntryResponse{event}})
	schemas[factorPreparationSchema] = responseDTOSchema("Pending authenticator seed and opaque token bound to the current session. Store in memory until confirmation.", factorPreparationResponse{
		Secret: syntheticTOTPSeed, Token: strings.Repeat("A", 80),
	})
}

func accountListSchema(description, item string, example any) *huma.Schema {
	return closedObjectSchema(description, map[string]*huma.Schema{
		privatePageItemsField: {Type: huma.TypeArray, Items: schemaRef(item)},
	}, []string{privatePageItemsField}, example)
}

func factorRequestBody(registry huma.Registry) *huma.RequestBody {
	const name = "AccountReplaceFactorRequest"
	prepare := closedObjectSchema("Prepare a replacement seed.", map[string]*huma.Schema{}, nil, map[string]any{})
	token := stringSchema("Opaque token returned by preparation in this full session.", strings.Repeat("A", 80))
	token.Pattern = "^[A-Za-z0-9_-]{80}$"
	code := credentialProperty(accountRoutes[accountFactorPath], credentialCodeField)
	confirmationExample := map[string]any{replacementTokenField: token.Examples[0], credentialCodeField: code.Examples[0]}
	confirm := closedObjectSchema("Confirm the authenticator using its new TOTP.", map[string]*huma.Schema{
		replacementTokenField: token, credentialCodeField: code,
	}, []string{replacementTokenField, credentialCodeField}, confirmationExample)
	registry.Map()[name] = &huma.Schema{Description: "Prepare a new authenticator or confirm it using the same full session.", OneOf: []*huma.Schema{prepare, confirm}, Examples: []any{map[string]any{}, confirmationExample}}
	return &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(name)}}}
}

func accountErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{400: {validationCode}, 401: {authenticationCode}, 403: {originCode}, 405: {methodNotAllowedCode}, 503: {securityUnavailableCode}}
	if rt.operationID == listAuditOperation {
		codes[403] = append(codes[403], permissionCode)
	}
	if rt.method == http.MethodPost {
		codes[403] = append(codes[403], csrfCode, stepUpRequiredCode)
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	if rt.operationID == revokeSessionOperation || rt.operationID == listTargetSessionsOperation {
		codes[404] = []string{notFoundCode}
	}
	if rt.operationID == listTargetSessionsOperation {
		codes[403] = append(codes[403], stepUpRequiredCode)
	}
	if rt.operationID == accountFactorOperation {
		codes[401] = append(codes[401], rejectedAuthenticationCode)
	}
	return codes
}

func adjustAccountResponse(rt route, response *huma.Response, schemas map[string]*huma.Schema) {
	switch rt.operationID {
	case accountPasswordOperation, accountCodesOperation, revokeAllSessionsOperation, revokeSessionOperation:
		response.Headers[setCookieHeader] = sessionCookieHeader()
	case accountFactorOperation:
		response.Description = "Preparation returns the pending seed. Confirmation returns ten new recovery codes once and clears all browser sessions."
		response.Headers[setCookieHeader] = sessionCookieHeader()
		response.Content[jsonMediaType] = &huma.MediaType{
			Schema: &huma.Schema{OneOf: []*huma.Schema{schemaRef(factorPreparationSchema), schemaRef(recoveryCodesSchema)}},
			Examples: map[string]*huma.Example{
				"prepared":  {Value: schemas[factorPreparationSchema].Examples[0]},
				"confirmed": {Value: schemas[recoveryCodesSchema].Examples[0]},
			},
		}
	}
}
