package platform

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/siteconfig"
)

const siteUnavailableCode = "site_configuration_unavailable"

type siteInput struct {
	Language string `query:"lang" doc:"Optional requested UI language (BCP 47); unavailable catalogs use the configured fallback. Matching is by normalized exact code." example:"pl"`
	Version  string `query:"version" doc:"Optional opaque UUID from a previous response; a different current publication returns 409. Historical publications cannot be requested." example:"33333333-3333-4333-8333-333333333333"`
}
type siteOutput struct{ Body siteconfig.Publication }

func registerSite(api huma.API, reader *siteconfig.Reader, logger *slog.Logger) {
	huma.Register(api, huma.Operation{Tags: []string{sitePublicationTag}, OperationID: "getSite", Method: http.MethodGet, Path: "/api/v1/site", Summary: "Read published site settings and one complete UI catalog",
		Description: "One atomic publication supplies settings, language-specific slug suggestion rules, UI languages and messages. Omitted lang uses the configured UI fallback. No publication or disabled default/fallback returns 503. Drafts and historical versions are private. The requested language is normalized and matched by exact code. An unavailable catalog uses fallback_language, and language.code identifies the actual catalog. messages contains a complete catalog from the same version. version is an optional consistency check, not a historical selector. Empty, repeated and unknown query parameters return 400. No session is required.",
		Responses:   siteResponses(api), Middlewares: huma.Middlewares{siteQuery(api)}}, func(ctx context.Context, input *siteInput) (*siteOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		value, err := reader.Read(ctx, input.Language, input.Version)
		if err != nil {
			return nil, siteProblem(ctx, logger, err)
		}
		return &siteOutput{Body: value}, nil
	})
}

func siteQuery(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		u := ctx.URL()
		// The site endpoint accepts an omitted lang and uses the published fallback.
		if !validPublicQuery(u.RawQuery, []string{"version"}) && !validPublicQuery(u.RawQuery, []string{"lang", "version"}) {
			ctx.SetHeader(contentTypeHeader, problemJSONMediaType)
			ctx.SetStatus(400)
			_ = api.Marshal(ctx.BodyWriter(), problemJSONMediaType, newProblem(400, invalidParametersCode))
			return
		}
		next(ctx)
	}
}

func siteProblem(ctx context.Context, logger *slog.Logger, err error) error {
	var typed *siteconfig.Error
	if errors.As(err, &typed) && (typed.Status == 400 || typed.Status == 409) {
		return newProblem(typed.Status, typed.Code)
	}
	code := siteUnavailableCode
	if typed != nil {
		code = typed.Code
	}
	state := ""
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		state = sqlState.SQLState()
	}
	logger.WarnContext(ctx, siteUnavailableCode, requestIDField, ctx.Value(requestIDKey{}), "code", code, "sql_state", state)
	return newProblem(503, siteUnavailableCode)
}

func siteResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, 400, 409, 503)
	responses["400"].Description = "Invalid or ambiguous query parameters. Codes invalid_parameters, invalid_language and invalid_version."
	responses["409"].Description = "The supplied version differs from the current publication, or a database read conflict occurred. Code site_version_conflict. Read again without version to obtain the current publication."
	responses["409"].Content[problemJSONMediaType].Example = newProblem(409, "site_version_conflict")
	responses["503"].Description = "No usable site publication, disabled default or fallback language, unavailable complete catalog, or dependency failure. Code site_configuration_unavailable."
	responses["503"].Content[problemJSONMediaType].Example = newProblem(503, siteUnavailableCode)
	language := siteconfig.Language{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: leftToRightDirection}
	value := siteconfig.Publication{
		Version:           "33333333-3333-4333-8333-333333333333",
		Settings:          apiexamples.PortalSettings(),
		RequestedLanguage: "kk",
		Language:          language,
		Languages:         []siteconfig.Language{language},
		Messages:          apiexamples.PortalMessageExample(),
		SlugRules:         []siteconfig.SlugRule{{LanguageCode: "pl", Character: "ł", Replacement: "l"}},
	}
	omitted := value
	omitted.RequestedLanguage = ""
	responses["200"] = &huma.Response{
		Description: "One atomic publication with settings, slug suggestion rules and a complete UI catalog, possibly using the configured fallback.",
		Content: map[string]*huma.MediaType{
			jsonMediaType: {
				Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[siteconfig.Publication](), true, "Publication"),
				Examples: map[string]*huma.Example{
					"fallback": {Summary: "Kazakh requested, Polish fallback returned", Description: "The example includes one message to illustrate the catalog shape.", Value: value},
					"omitted":  {Summary: "Polish fallback returned when lang is omitted", Description: "The example includes one message to illustrate the catalog shape.", Value: omitted},
				},
			},
		},
	}
	return responses
}
