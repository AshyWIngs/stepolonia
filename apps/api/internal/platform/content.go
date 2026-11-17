package platform

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
)

const (
	problemJSONMediaType    = "application/problem+json"
	databaseUnavailableCode = "database_unavailable"
	contentTypeHeader       = "Content-Type"
	locationHeader          = "Location"
	languageQueryKey        = "language"
	limitQueryKey           = "limit"
	offsetQueryKey          = "offset"
)

type languageDTO struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Direction  string `json:"direction" enum:"ltr,rtl"`
}

type contentListInput struct {
	TypeKey  string `path:"type_key"`
	Language string `query:"language" required:"true" doc:"Content language (BCP 47)."`
	Limit    int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
	Offset   int    `query:"offset" default:"0" minimum:"0" maximum:"10000"`
}
type contentInput struct {
	TypeKey  string `path:"type_key"`
	Slug     string `path:"slug"`
	Language string `query:"language" required:"true"`
}
type languagesOutput struct {
	Body []languageDTO `nullable:"false"`
}
type contentListOutput struct{ Body content.PublicPage }
type contentOutput struct {
	Status   int
	Location string              `header:"Location"`
	Body     *content.PublicItem `nullable:"false"`
}

type publicProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

func (p *publicProblem) Error() string             { return p.Code }
func (p *publicProblem) GetStatus() int            { return p.Status }
func (p *publicProblem) ContentType(string) string { return problemJSONMediaType }

func newProblem(status int, code string) *publicProblem {
	detail := "The request parameters are invalid."
	switch status {
	case http.StatusConflict:
		detail = "The published version has changed."
	case http.StatusNotFound:
		detail = "The requested resource was not found."
	case http.StatusServiceUnavailable:
		detail = "A required dependency is unavailable."
	}
	return &publicProblem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code}
}

func problemResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	result := make(map[string]*huma.Response, len(statuses))
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[publicProblem](), true, "PublicProblem")
	for _, status := range statuses {
		result[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status), Content: map[string]*huma.MediaType{problemJSONMediaType: {Schema: schema}}}
	}
	return result
}

func registerContent(api huma.API, reader *content.PublicReader, logger *slog.Logger) {
	huma.Register(api, huma.Operation{
		OperationID: "listLanguages", Method: http.MethodGet, Path: "/api/v1/languages",
		Summary:   "List active content languages",
		Responses: problemResponses(api, 400, 503), Middlewares: huma.Middlewares{publicQuery(api)},
	}, func(ctx context.Context, _ *struct{}) (*languagesOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		languages, err := reader.ListLanguages(ctx)
		if err != nil {
			return nil, contentProblem(ctx, logger, err)
		}
		out := &languagesOutput{Body: make([]languageDTO, 0, len(languages))}
		for _, value := range languages {
			out.Body = append(out.Body, languageDTO{Code: value.Code, Name: value.Name, NativeName: value.NativeName, Direction: value.Direction})
		}
		return out, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listContent", Method: http.MethodGet, Path: "/api/v1/content/{type_key}",
		Summary: "List published collection content", Description: "Publication time and item ID descending. No content language fallback or field predicates.",
		Responses: problemResponses(api, 400, 404, 409, 503), Middlewares: huma.Middlewares{publicQuery(api, languageQueryKey, limitQueryKey, offsetQueryKey)},
	}, func(ctx context.Context, input *contentListInput) (*contentListOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		page, err := reader.List(ctx, input.TypeKey, input.Language, content.Page{Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return nil, contentProblem(ctx, logger, err)
		}
		return &contentListOutput{Body: page}, nil
	})
	responses := problemResponses(api, 400, 404, 409, 503)
	responses["301"] = &huma.Response{Description: "Historical slug; direct canonical API address in the same language.", Headers: map[string]*huma.Param{locationHeader: {Required: true, Schema: &huma.Schema{Type: "string"}}}, Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: huma.TypeNull}}}}
	huma.Register(api, huma.Operation{
		OperationID: "getContent", Method: http.MethodGet, Path: "/api/v1/content/{type_key}/{slug}", Summary: "Get published content or its canonical address",
		Responses: responses, Middlewares: huma.Middlewares{publicQuery(api, languageQueryKey)},
	}, func(ctx context.Context, input *contentInput) (*contentOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resolved, err := reader.Resolve(ctx, input.TypeKey, input.Language, input.Slug)
		if err != nil {
			return nil, contentProblem(ctx, logger, err)
		}
		if resolved.Redirect {
			return &contentOutput{Status: http.StatusMovedPermanently, Location: "/api/v1/content/" + url.PathEscape(resolved.Item.TypeKey) + "/" + url.PathEscape(resolved.Item.Slug) + "?" + url.Values{languageQueryKey: {resolved.Item.Language}}.Encode()}, nil
		}
		return &contentOutput{Status: http.StatusOK, Body: &resolved.Item}, nil
	})
}

// Reject ambiguous queries before Huma parsing so malformed input consistently returns 400.
func publicQuery(api huma.API, allowed ...string) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		u := ctx.URL()
		if !validPublicQuery(u.RawQuery, allowed) {
			ctx.SetHeader(contentTypeHeader, problemJSONMediaType)
			ctx.SetStatus(http.StatusBadRequest)
			_ = api.Marshal(ctx.BodyWriter(), problemJSONMediaType, newProblem(http.StatusBadRequest, "invalid_parameters"))
			return
		}
		next(ctx)
	}
}

func validPublicQuery(raw string, allowed []string) bool {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	for name, values := range query {
		if !slices.Contains(allowed, name) || len(values) != 1 || values[0] == "" {
			return false
		}
	}
	if slices.Contains(allowed, languageQueryKey) && query.Get(languageQueryKey) == "" {
		return false
	}
	return validPageParameter(query, limitQueryKey, 1, 100) && validPageParameter(query, offsetQueryKey, 0, 10000)
}

func validPageParameter(query url.Values, name string, minimum, maximum int) bool {
	if !query.Has(name) {
		return true
	}
	value, err := strconv.Atoi(query.Get(name))
	return err == nil && value >= minimum && value <= maximum
}

func contentProblem(ctx context.Context, logger *slog.Logger, err error) error {
	var typed *content.Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case content.Validation:
			return newProblem(http.StatusBadRequest, typed.Code)
		case content.NotFound:
			return newProblem(http.StatusNotFound, typed.Code)
		case content.Conflict:
			return newProblem(http.StatusConflict, typed.Code)
		}
	}
	logDependency(ctx, logger, err)
	return newProblem(http.StatusServiceUnavailable, databaseUnavailableCode)
}

func logDependency(ctx context.Context, logger *slog.Logger, err error) {
	code, sqlState := databaseUnavailableCode, ""
	var typed *content.Error
	if errors.As(err, &typed) {
		code = typed.Code
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		sqlState = state.SQLState()
	}
	logger.WarnContext(ctx, "dependency_unavailable", requestIDField, ctx.Value(requestIDKey{}), "code", code, "sql_state", sqlState)
}
