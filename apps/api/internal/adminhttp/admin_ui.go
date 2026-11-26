package adminhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/identity"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	adminEntryPath        = "/api/v1/admin/ui/entry"
	adminCatalogPath      = "/api/v1/admin/ui/catalog"
	adminEntryOperation   = "adminUIEntry"
	adminCatalogOperation = "adminUICatalog"
	adminUITimeout        = 3 * time.Second
	siteUnavailableCode   = "site_configuration_unavailable"
	adminUITag            = "administration-interface"
	adminEntrySchema      = "AdminEntry"
	adminCatalogSchema    = "AdminCatalog"
)

var adminUIRoutes = map[string][]route{
	adminEntryPath: {{
		method: http.MethodGet, operationID: adminEntryOperation, response: adminEntrySchema,
		summary:     "Read the administration sign-in catalog",
		description: "Read-only published branding, language choices and the texts used for sign-in, MFA, enrollment and recovery. No session or Origin header is needed, including for server-side rendering. Send no body. Omit lang to select the installation fallback.",
	}},
	adminCatalogPath: {{
		kind: fullKind, method: http.MethodGet, operationID: adminCatalogOperation, response: adminCatalogSchema,
		summary:     "Read the published administration workspace catalog",
		description: "Returns the published workspace catalog and default content language for the verified account. Published slug suggestions require settings.manage, content_types.manage, or a create or update grant on an enabled content type. Other sessions receive an empty slug_rules array. Send no body. Omit lang to select the installation fallback.",
	}},
}

type adminUIReader interface {
	ReadAdminUI(context.Context, identity.AuthRequest, func(context.Context, identity.Session, bool) error) error
}

type adminCatalogResponse struct {
	siteconfig.AdminCatalog
	AccountID string `json:"account_id" format:"uuid" doc:"Account confirmed by the full session. The workspace reinitializes when a browser switches accounts."`
}

func (h *handler) serveAdminEntry(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != rt.method {
		w.Header().Set("Allow", rt.method)
		problem(w, http.StatusMethodNotAllowed, methodNotAllowedCode)
		return
	}
	lang, err := adminUIQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminUITimeout)
	defer cancel()
	entry, err := h.publishedSite.ReadAdminEntry(ctx, lang)
	if err != nil {
		writeAdminUIError(w, err)
		return
	}
	respond(w, entry)
}

func (h *handler) serveAdminCatalog(w http.ResponseWriter, r *http.Request, rt route, ip string) {
	req, ok := requestWithID(w, r, rt.kind, ip)
	if !ok {
		return
	}
	lang, err := adminUIQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	service, ok := h.service.(adminUIReader)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminUITimeout)
	defer cancel()
	var result adminCatalogResponse
	err = service.ReadAdminUI(ctx, req, func(ctx context.Context, actor identity.Session, mayReadSlugRules bool) error {
		var err error
		result.AccountID = actor.UserID()
		result.AdminCatalog, err = h.publishedSite.ReadAdminCatalog(ctx, lang, mayReadSlugRules)
		return err
	})
	if err != nil {
		writeAdminUIError(w, err)
		return
	}
	respond(w, result)
}

func adminUIQuery(r *http.Request) (string, error) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.URL.ForceQuery {
		return "", identity.ErrValidation
	}
	if r.URL.RawQuery == "" {
		return "", nil
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	lang := query["lang"]
	if err != nil || strings.Contains(r.URL.RawQuery, "&") || len(query) != 1 || len(lang) != 1 || lang[0] == "" {
		return "", identity.ErrValidation
	}
	return lang[0], nil
}

func writeAdminUIError(w http.ResponseWriter, err error) {
	var publication *siteconfig.Error
	if errors.As(err, &publication) {
		if publication.Status == http.StatusBadRequest {
			writeError(w, identity.ErrValidation)
		} else {
			problem(w, http.StatusServiceUnavailable, siteUnavailableCode)
		}
		return
	}
	writeError(w, err)
}

func adminUIOperation(rt route, example any) *huma.Operation {
	entry := rt.operationID == adminEntryOperation
	description := rt.description
	var parameters []*huma.Param
	var security []map[string][]string
	codes := map[int][]string{
		400: {validationCode}, 405: {methodNotAllowedCode}, 503: {siteUnavailableCode},
	}
	if !entry {
		description += " " + boundaryDescription
		parameters = authParameters(rt)
		security = authSecurity(rt)
		codes[401] = []string{authenticationCode}
		codes[403] = []string{originCode}
		codes[503] = append(codes[503], securityUnavailableCode)
	}
	parameters = append(parameters, &huma.Param{
		Name: "lang", In: openAPIQueryParameter, Schema: languageCodeSchema(),
		Description: "Optional BCP 47 UI language. Unavailable catalogs use the published fallback. Empty, duplicate and unknown query parameters return 400.",
	})
	responses := map[string]*huma.Response{
		"200": {Description: rt.summary + ".", Headers: authResponseHeaders(),
			Content: map[string]*huma.MediaType{jsonMediaType: {Schema: schemaRef(rt.response), Example: example}}},
	}
	for status, reasons := range codes {
		responses[strconv.Itoa(status)] = authErrorResponse(rt, status, reasons)
	}
	return &huma.Operation{OperationID: rt.operationID, Summary: rt.summary, Description: description,
		Tags: []string{adminUITag}, Parameters: parameters, Responses: responses, Security: security}
}

func addAdminUISchemas(schemas map[string]*huma.Schema) {
	entry := siteconfig.AdminEntry{
		Version: exampleUUID, Title: apiexamples.PortalSettings().Title, Language: languageExample,
		Languages: []siteconfig.Language{languageExample}, Messages: map[string]string{"admin.sign_in": "Zaloguj się"},
	}
	catalog := siteconfig.AdminCatalog{AdminEntry: entry, DefaultLanguage: "pl", SlugRules: []siteconfig.SlugRule{slugRuleExample()}}
	catalog.Messages = map[string]string{"admin.sign_in": "Zaloguj się", "articles": "Materiały"}
	schemas[adminEntrySchema] = responseDTOSchema("Published interface texts used before a full session. This example shows one key.", entry)
	schemas[adminCatalogSchema] = responseDTOSchema("Published workspace catalog, naming defaults and verified account. This example shows two keys.",
		adminCatalogResponse{AdminCatalog: catalog, AccountID: exampleUUID})
}
