package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

const (
	sessionPolicyPath             = "/api/v1/admin/security/session-policy"
	readSessionPolicyOperation    = "readSessionPolicy"
	replaceSessionPolicyOperation = "replaceSessionPolicy"
	sessionPolicySchema           = "SessionPolicy"
	sessionPolicyTag              = "security-policy"
)

type sessionPolicyService interface {
	ReadSessionPolicy(context.Context, identity.AuthRequest) (identity.SessionPolicy, error)
	ReplaceSessionPolicy(context.Context, identity.AuthRequest, identity.SessionPolicy) (identity.SessionPolicy, error)
}

var _ sessionPolicyService = (*identity.AuthService)(nil)

var sessionPolicyRoutes = []route{
	{kind: fullKind, method: http.MethodGet, operationID: readSessionPolicyOperation, summary: "Read the installation session policy", response: sessionPolicySchema,
		description: "Requires an active full session and security_policy.manage. Returns the limits that apply to future full sign-ins."},
	{kind: fullKind, method: http.MethodPut, operationID: replaceSessionPolicyOperation, summary: "Replace the installation session policy", response: sessionPolicySchema,
		description: "Requires an active full session, security_policy.manage, CSRF and fresh password and TOTP authentication. Replaces the policy under the expected version and audits the change atomically. Existing sessions retain their original limits."},
}

type sessionPolicyInput struct {
	ExpectedVersion      int64 `json:"expected_version,string"`
	IdleTimeoutMinutes   int   `json:"idle_timeout_minutes"`
	AbsoluteTimeoutHours int   `json:"absolute_timeout_hours"`
}

var sessionPolicyRequestContract = sessionPolicyRequestSchema()

func sessionPolicyRequestSchema() *huma.Schema {
	example := sessionPolicyInput{
		ExpectedVersion: 1, IdleTimeoutMinutes: identity.DefaultSessionIdleMinutes, AbsoluteTimeoutHours: identity.DefaultSessionHours,
	}
	schema := responseDTOSchema("Complete lifetime policy for newly issued full sessions. The idle timeout must not exceed the absolute lifetime.", example)
	schema.AdditionalProperties = false
	schema.Properties[expectedVersionField] = contentVersionSchema("The version returned with the current policy.", "1")
	setPolicyBounds(schema)
	schema.PrecomputeMessages()
	return schema
}

func sessionPolicyResponseSchema() *huma.Schema {
	example := identity.SessionPolicy{Version: 1, IdleTimeoutMinutes: identity.DefaultSessionIdleMinutes, AbsoluteTimeoutHours: identity.DefaultSessionHours}
	schema := responseDTOSchema("Installation session policy. Existing sessions retain their original limits.", example)
	schema.Properties["version"] = contentVersionSchema("Current policy version.", "1")
	setPolicyBounds(schema)
	schema.PrecomputeMessages()
	return schema
}

func setPolicyBounds(schema *huma.Schema) {
	minIdle, maxIdle := float64(identity.MinSessionIdleMinutes), float64(identity.MaxSessionIdleMinutes)
	minHours, maxHours := float64(identity.MinSessionHours), float64(identity.MaxSessionHours)
	idle, absolute := schema.Properties["idle_timeout_minutes"], schema.Properties["absolute_timeout_hours"]
	idle.Minimum, idle.Maximum = &minIdle, &maxIdle
	absolute.Minimum, absolute.Maximum = &minHours, &maxHours
}

func (h *handler) serveSessionPolicy(w http.ResponseWriter, r *http.Request, rt route, ip string) {
	service, ok := h.service.(sessionPolicyService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if rt.method == http.MethodPut {
		writeSessionPolicy(w, r, rt, ip, service)
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, identity.ErrValidation)
		return
	}
	req, _, err := requestSession(r, fullKind, ip)
	if err != nil {
		writeError(w, err)
		return
	}
	out, err := service.ReadSessionPolicy(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, out)
}

func writeSessionPolicy(w http.ResponseWriter, r *http.Request, rt route, ip string, service sessionPolicyService) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	var input sessionPolicyInput
	if !decodeSessionPolicy(raw, &input) {
		writeError(w, identity.ErrValidation)
		return
	}
	out, err := service.ReplaceSessionPolicy(r.Context(), req, identity.SessionPolicy{
		Version: input.ExpectedVersion, IdleTimeoutMinutes: input.IdleTimeoutMinutes, AbsoluteTimeoutHours: input.AbsoluteTimeoutHours,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, out)
}

func decodeSessionPolicy(raw []byte, input *sessionPolicyInput) bool {
	if !utf8.Valid(raw) {
		return false
	}
	value, ok := decodeUniqueJSON(raw)
	schema := sessionPolicyRequestContract
	if !ok || !exactDraftProperties(value, schema) {
		return false
	}
	var result huma.ValidateResult
	huma.Validate(nil, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	return len(result.Errors) == 0 && json.Unmarshal(raw, input) == nil && input.ExpectedVersion > 0
}

func sessionPolicyErrorCodes(rt route) map[int][]string {
	codes := map[int][]string{400: {validationCode}, 401: {authenticationCode}, 403: {originCode, permissionCode},
		405: {methodNotAllowedCode}, 503: {securityUnavailableCode}}
	if rt.method == http.MethodPut {
		codes[403] = append(codes[403], csrfCode, stepUpRequiredCode)
		codes[409] = []string{versionConflictCode}
		codes[413] = []string{bodyTooLargeCode}
		codes[429] = []string{rateLimitedCode}
	}
	return codes
}
