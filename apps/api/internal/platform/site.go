package platform

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/siteconfig"
)

const siteUnavailableCode = "site_configuration_unavailable"

type siteInput struct {
	Language string `query:"lang" doc:"Optional requested UI language (BCP 47); unavailable catalogs use the configured fallback."`
	Version  string `query:"version" doc:"Optional opaque UUID from a previous response; a different current publication returns 409."`
}
type siteOutput struct{ Body siteconfig.Publication }

func registerSite(api huma.API, reader *siteconfig.Reader, logger *slog.Logger) {
	huma.Register(api, huma.Operation{OperationID: "getSite", Method: http.MethodGet, Path: "/api/v1/site", Summary: "Read published site settings and one complete UI catalog",
		Description: "One atomic publication supplies settings, UI languages and messages. Omitted lang uses the configured UI fallback. No publication or disabled default/fallback returns 503. Drafts and historical versions are private.",
		Responses:   problemResponses(api, 400, 409, 503), Middlewares: huma.Middlewares{siteQuery(api)}}, func(ctx context.Context, input *siteInput) (*siteOutput, error) {
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
		// Общий query middleware требует lang для статей; здесь параметр намеренно необязателен.
		if !validPublicQuery(u.RawQuery, []string{"version"}) && !validPublicQuery(u.RawQuery, []string{"lang", "version"}) {
			ctx.SetHeader(contentTypeHeader, problemJSONMediaType)
			ctx.SetStatus(400)
			_ = api.Marshal(ctx.BodyWriter(), problemJSONMediaType, newProblem(400, "invalid_parameters"))
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
