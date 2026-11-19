package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/content"
)

const leftToRightDirection = "ltr"

type languageDTO struct {
	Code       string `json:"code" doc:"Normalized BCP 47 content language code." example:"pl"`
	Name       string `json:"name" doc:"Human-readable display name of the language." example:"Polish"`
	NativeName string `json:"native_name" doc:"Language name in its own language." example:"Polski"`
	Direction  string `json:"direction" enum:"ltr,rtl" doc:"Writing direction used when rendering this language." example:"ltr"`
}

const (
	languageQueryKey = "language"
	limitQueryKey    = "limit"
	offsetQueryKey   = "offset"
)

type contentListInput struct {
	TypeKey  string `path:"type_key" doc:"Public Registry collection key. Unknown or non-public collections return 404." example:"article"`
	Language string `query:"language" required:"true" doc:"Required BCP 47 content language, normalized by the server. No content fallback is performed." example:"pl"`
	Limit    int    `query:"limit" default:"20" minimum:"1" maximum:"100" doc:"Maximum number of items returned. Defaults to 20." example:"20"`
	Offset   int    `query:"offset" default:"0" minimum:"0" maximum:"10000" doc:"Number of ordered items to skip. Follow next_offset for subsequent pages. Defaults to zero." example:"0"`
}
type contentInput struct {
	TypeKey  string `path:"type_key" doc:"Public Registry collection key. Unknown or non-public collections return 404." example:"article"`
	Slug     string `path:"slug" doc:"Current or historical slug in the requested language, at most 200 characters. Historical slugs return a canonical redirect."`
	Language string `query:"language" required:"true" doc:"Required BCP 47 content language. The exact published translation is resolved, with no fallback." example:"pl"`
}
type languagesOutput struct {
	Body []languageDTO `nullable:"false"`
}
type contentListOutput struct{ Body content.PublicPage }

const locationHeader = "Location"

type contentOutput struct {
	Status   int
	Location string              `header:"Location" doc:"Canonical API address in the same content language, present on historical-slug 301 responses."`
	Body     *content.PublicItem `nullable:"false"`
}

func registerContent(api huma.API, reader *content.PublicReader, logger *slog.Logger) {
	huma.Register(api, huma.Operation{
		Tags: []string{contentLanguagesTag}, OperationID: "listLanguages", Method: http.MethodGet, Path: "/api/v1/languages",
		Summary:     "List active content languages",
		Description: "Returns active content languages ordered by language code. The array may be empty. This endpoint accepts no query parameters and requires no session. UI catalog availability is reported separately by getSite.",
		Responses:   languageResponses(api), Middlewares: huma.Middlewares{publicQuery(api)},
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
		Tags: []string{publishedContentTag}, OperationID: "listContent", Method: http.MethodGet, Path: "/api/v1/content/{type_key}",
		Summary: "List published collection content", Description: "Lists public published items in an active content language, ordered by publication time descending and then item UUID descending. language is required. limit defaults to 20 and offset to zero. Follow next_offset until null, subject to the maximum offset of 10000. A valid empty collection returns items []. Content language fallback and field predicates are unavailable. Private fields are excluded before pagination. Unknown, repeated and empty query parameters return 400. No session is required.",
		Responses: contentResponses(api, true), Middlewares: huma.Middlewares{publicQuery(api, languageQueryKey, limitQueryKey, offsetQueryKey)},
	}, func(ctx context.Context, input *contentListInput) (*contentListOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		page, err := reader.List(ctx, input.TypeKey, input.Language, content.Page{Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return nil, contentProblem(ctx, logger, err)
		}
		return &contentListOutput{Body: page}, nil
	})
	responses := contentResponses(api, false)
	canonical := apiexamples.Article()
	canonicalAPI := "/api/v1/content/" + url.PathEscape(canonical.TypeKey) + "/" + url.PathEscape(canonical.Slug) + "?" + url.Values{languageQueryKey: {canonical.Language}}.Encode()
	responses["301"] = &huma.Response{Description: "Historical slug; direct canonical API address in the same language.", Headers: map[string]*huma.Param{locationHeader: {Required: true, Description: "Direct canonical API address in the same language.", Schema: &huma.Schema{Type: huma.TypeString, Examples: []any{canonicalAPI}}}}, Content: map[string]*huma.MediaType{jsonMediaType: {Schema: &huma.Schema{Type: huma.TypeNull}, Examples: map[string]*huma.Example{"redirect": {Summary: "Historical slug resolved by the Location header", Value: json.RawMessage(`null`)}}}}}
	huma.Register(api, huma.Operation{
		Tags: []string{publishedContentTag}, OperationID: "getContent", Method: http.MethodGet, Path: "/api/v1/content/{type_key}/{slug}", Summary: "Get published content or its canonical address",
		Description: "Resolves the current public translation for type_key, slug and required language. There is no language fallback. Current slugs return public scalar fields, resolvable same-language relations and their available labels from the sealed publication. Historical slugs return 301 with a direct canonical API Location and JSON null body. Unpublished or unavailable translations return 404. Unknown, repeated and empty query parameters return 400. No session is required.",
		Responses:   responses, Middlewares: huma.Middlewares{publicQuery(api, languageQueryKey)},
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
			_ = api.Marshal(ctx.BodyWriter(), problemJSONMediaType, newProblem(http.StatusBadRequest, invalidParametersCode))
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

// TransformSchema documents language metadata without changing validation.
func (languageDTO) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "An active content language. Content availability and complete UI catalog availability are independent."
	return schema
}

const emptyExampleName = "empty"

func languageResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, 400, 503)
	responses["400"].Description = "This operation accepts no query parameters. Unknown, repeated, empty or malformed parameters return code invalid_parameters."
	responses["200"] = &huma.Response{
		Description: "Active content languages ordered by code, including an empty array when none are active.",
		Content: map[string]*huma.MediaType{
			jsonMediaType: {
				Examples: map[string]*huma.Example{
					"active": {
						Summary: "Two active content languages",
						Value: []languageDTO{
							{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: leftToRightDirection},
							{Code: "ru", Name: "Russian", NativeName: "Русский", Direction: leftToRightDirection},
						},
					},
					emptyExampleName: {Summary: "No active content languages", Value: []languageDTO{}},
				},
			},
		},
	}
	return responses
}

func contentResponses(api huma.API, list bool) map[string]*huma.Response {
	responses := problemResponses(api, 400, 404, 409, 503)
	article := apiexamples.Article()
	event := apiexamples.Event()
	examples := map[string]*huma.Example{
		"article": {Summary: "Published Article with exact-language public fields", Value: article},
		"event":   {Summary: "Published Event with boolean, exact decimal and same-language Article relation", Value: event},
	}
	if list {
		event.FieldLabels = nil
		next := 1
		examples = map[string]*huma.Example{
			"page":           {Summary: "First page with limit=1 and another item available", Value: content.PublicPage{Items: []content.PublicItem{apiexamples.SecondArticle()}, NextOffset: &next}},
			"last":           {Summary: "Final page with next_offset null", Value: content.PublicPage{Items: []content.PublicItem{article}}},
			emptyExampleName: {Summary: "Empty published collection", Value: content.PublicPage{Items: []content.PublicItem{}}},
			"events":         {Summary: "Event collection with a public relation to an Article", Value: content.PublicPage{Items: []content.PublicItem{event}}},
		}
	}
	responses["200"] = &huma.Response{
		Description: "Published public content from the current publication in the exact requested language.",
		Content: map[string]*huma.MediaType{
			jsonMediaType: {Examples: examples},
		},
	}
	return responses
}
