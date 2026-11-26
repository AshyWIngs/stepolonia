package adminhttp

import (
	"context"
	"maps"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

const (
	ratePolicyPath             = "/api/v1/admin/security/rate-policy"
	readRatePoliciesOperation  = "readRatePolicies"
	replaceRatePolicyOperation = "replaceRatePolicy"
	ratePoliciesSchema         = "RatePolicies"
	ratePolicySchema           = "RatePolicy"
	ratePolicyBoundsSchema     = "RatePolicyBounds"
	ratePolicyRangeSchema      = "RatePolicyRange"
	rateSubjectField           = "subject_attempts"
	rateHashJobsField          = "hash_jobs"
)

type ratePolicyService interface {
	ReadRatePolicies(context.Context, identity.AuthRequest) ([]identity.RatePolicy, error)
	ReplaceRatePolicy(context.Context, identity.AuthRequest, identity.RatePolicy) (identity.RatePolicy, error)
}

var ratePolicyRoutes = []route{
	{kind: fullKind, method: http.MethodGet, operationID: readRatePoliciesOperation, summary: "Read attempt limits", response: ratePoliciesSchema,
		description: "Requires a full session and security_policy.manage. Returns independent rolling limits and versions for credentials, preauthentication, authenticated step-up and denial audits."},
	{kind: fullKind, method: http.MethodPost, operationID: replaceRatePolicyOperation, summary: "Change an attempt limit policy", response: ratePolicySchema,
		description: "Requires a full session, security_policy.manage, CSRF and fresh password and TOTP authentication. Updates the selected scope under the expected version and audits the change atomically. Existing attempts remain counted according to the new window."},
}

type ratePolicyInput struct {
	Scope                string `json:"scope"`
	ExpectedVersion      int64  `json:"expected_version,string"`
	WindowSeconds        int    `json:"window_seconds"`
	SubjectAttempts      *int   `json:"subject_attempts,omitempty"`
	IPAttempts           int    `json:"ip_attempts"`
	InstallationAttempts int    `json:"installation_attempts"`
	HashJobs             *int   `json:"hash_jobs,omitempty"`
}

type ratePolicyRange struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

type ratePolicyBounds struct {
	Window       ratePolicyRange `json:"window_seconds"`
	Subject      ratePolicyRange `json:"subject_attempts"`
	IP           ratePolicyRange `json:"ip_attempts"`
	Installation ratePolicyRange `json:"installation_attempts"`
	HashJobs     ratePolicyRange `json:"hash_jobs"`
}

var rateBounds = ratePolicyBounds{
	Window: ratePolicyRange{identity.MinRateWindowSeconds, identity.MaxRateWindowSeconds}, Subject: ratePolicyRange{1, identity.MaxSubjectAttempts},
	IP: ratePolicyRange{1, identity.MaxRateIPAttempts}, Installation: ratePolicyRange{1, identity.MaxRateSiteAttempts},
	HashJobs: ratePolicyRange{1, identity.MaxPasswordHashJobs},
}

type ratePolicyList struct {
	Policies []identity.RatePolicy `json:"policies"`
	Bounds   ratePolicyBounds      `json:"bounds"`
}

var ratePolicyContract = ratePolicyInputSchema()

func ratePolicyExample() identity.RatePolicy {
	return identity.RatePolicy{
		Scope: identity.RateCredentials, Version: 1, WindowSeconds: identity.DefaultRateWindowSeconds,
		SubjectAttempts: intPointer(identity.DefaultSubjectAttempts), IPAttempts: identity.DefaultIPAttempts, InstallationAttempts: identity.DefaultSiteAttempts,
		HashJobs: intPointer(identity.MaxPasswordHashJobs),
	}
}

func ratePolicyInputSchema() *huma.Schema {
	p := ratePolicyExample()
	example := ratePolicyInput{p.Scope, p.Version, p.WindowSeconds, p.SubjectAttempts, p.IPAttempts, p.InstallationAttempts, p.HashJobs}
	schema := responseDTOSchema("Rolling policy for one scope. Subject attempts are required except for preauth. Hash jobs are required for credentials and omitted for other scopes.", example)
	schema.Properties[expectedVersionField] = contentVersionSchema("The version of this selected policy.", "1")
	finishRatePolicySchema(schema)
	return schema
}

func addRatePolicySchemas(schemas map[string]*huma.Schema) {
	example := ratePolicyExample()
	schema := responseDTOSchema("Rolling window parameters and version of the selected policy.", example)
	schema.Properties["version"] = contentVersionSchema("Current rate policy version.", "1")
	finishRatePolicySchema(schema)
	schemas[ratePolicySchema] = schema
	schemas[ratePolicyBoundsSchema] = responseDTOSchema("Allowed editing ranges for the fields of independent attempt policies.", rateBounds)
	schemas[ratePolicyRangeSchema] = responseDTOSchema("Inclusive minimum and maximum accepted values for a policy field.", rateBounds.Window)
	list := responseDTOSchema("Independent rate policies and allowed editing ranges.", ratePolicyList{Policies: []identity.RatePolicy{example}, Bounds: rateBounds})
	list.Properties["policies"] = &huma.Schema{Type: huma.TypeArray, Items: schemaRef(ratePolicySchema)}
	schemas[ratePoliciesSchema] = list
}

func finishRatePolicySchema(schema *huma.Schema) {
	schema.AdditionalProperties = false
	schema.Properties["scope"].Enum = []any{identity.RateCredentials, identity.RatePreauth, identity.RateStepUp, identity.RateDenialAudit}
	for name, bounds := range map[string]ratePolicyRange{
		"window_seconds": rateBounds.Window, rateSubjectField: rateBounds.Subject,
		"ip_attempts": rateBounds.IP, "installation_attempts": rateBounds.Installation,
		rateHashJobsField: rateBounds.HashJobs,
	} {
		minimum, maximum := float64(bounds.Minimum), float64(bounds.Maximum)
		schema.Properties[name].Minimum = &minimum
		schema.Properties[name].Maximum = &maximum
		schema.Properties[name].Nullable = false
	}
	for _, scope := range []string{identity.RateCredentials, identity.RatePreauth, identity.RateStepUp, identity.RateDenialAudit} {
		shape := &huma.Schema{
			Type: huma.TypeObject, Properties: maps.Clone(schema.Properties), Required: slices.Clone(schema.Required), AdditionalProperties: false,
		}
		shape.Properties["scope"] = &huma.Schema{Type: huma.TypeString, Const: scope}
		configureRateField(shape, rateSubjectField, scope != identity.RatePreauth)
		configureRateField(shape, rateHashJobsField, scope == identity.RateCredentials)
		schema.OneOf = append(schema.OneOf, shape)
	}
	schema.PrecomputeMessages()
}

func configureRateField(schema *huma.Schema, field string, required bool) {
	if required {
		schema.Required = append(schema.Required, field)
	} else {
		delete(schema.Properties, field)
	}
}

func (h *handler) serveRatePolicy(w http.ResponseWriter, r *http.Request, rt route, ip string) {
	service, ok := h.service.(ratePolicyService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if rt.method == http.MethodPost {
		writeRatePolicy(w, r, rt, ip, service)
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
	policies, err := service.ReadRatePolicies(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, ratePolicyList{Policies: policies, Bounds: rateBounds})
}

func writeRatePolicy(w http.ResponseWriter, r *http.Request, rt route, ip string, service ratePolicyService) {
	req, ok := replacementRequest(w, r, rt, ip)
	if !ok {
		return
	}
	raw, code, status := readJSONBody(w, r, maxCredentialBodyBytes)
	if code != "" {
		problem(w, status, code)
		return
	}
	var input ratePolicyInput
	if !decodeRatePolicy(raw, &input) {
		writeError(w, identity.ErrValidation)
		return
	}
	out, err := service.ReplaceRatePolicy(r.Context(), req, identity.RatePolicy{
		Scope: input.Scope, Version: input.ExpectedVersion, WindowSeconds: input.WindowSeconds, SubjectAttempts: input.SubjectAttempts,
		IPAttempts: input.IPAttempts, InstallationAttempts: input.InstallationAttempts,
		HashJobs: input.HashJobs,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, out)
}

func decodeRatePolicy(raw []byte, input *ratePolicyInput) bool {
	return decodeClosedCommand(raw, ratePolicyContract, input) &&
		(input.SubjectAttempts == nil) == (input.Scope == identity.RatePreauth) &&
		(input.HashJobs != nil) == (input.Scope == identity.RateCredentials)
}
