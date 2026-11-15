package platform

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/publishing"
)

const (
	problemJSONMediaType    = "application/problem+json"
	databaseUnavailableCode = "database_unavailable"
	contentTypeHeader       = "Content-Type"
	locationHeader          = "Location"
	languageQueryKey        = "lang"
	limitQueryKey           = "limit"
	offsetQueryKey          = "offset"
)

type languageDTO struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Direction  string `json:"direction" enum:"ltr,rtl"`
}

type articleDTO struct {
	ArticleID    string `json:"article_id" format:"uuid"`
	LanguageCode string `json:"language_code"`
	Title        string `json:"title"`
	Slug         string `json:"slug"`
	BodyMarkdown string `json:"body_markdown"`
}

type publishedTranslationDTO struct {
	LanguageCode string `json:"language_code"`
	Slug         string `json:"slug"`
}

type articleDetailDTO struct {
	ArticleID             string                    `json:"article_id" format:"uuid"`
	LanguageCode          string                    `json:"language_code"`
	Title                 string                    `json:"title"`
	Slug                  string                    `json:"slug"`
	BodyMarkdown          string                    `json:"body_markdown"`
	PublishedTranslations []publishedTranslationDTO `json:"published_translations" nullable:"false" doc:"Published translations of this article in enabled content languages, ordered by language code. Each slug is canonical for its language."`
}

type articleListInput struct {
	Language string `query:"lang" required:"true" doc:"Content language (BCP 47)."`
	Limit    int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
	Offset   int    `query:"offset" default:"0" minimum:"0"`
}

type articleInput struct {
	Slug     string `path:"slug"`
	Language string `query:"lang" required:"true" doc:"Content language (BCP 47)."`
}

type languagesOutput struct {
	Body []languageDTO `nullable:"false"`
}

type articlesOutput struct {
	Body []articleDTO `nullable:"false"`
}

type articleOutput struct {
	Status   int
	Location string            `header:"Location"`
	Body     *articleDetailDTO `nullable:"false"`
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

func registerPublishing(api huma.API, reader *publishing.PublicReader, logger *slog.Logger) {
	huma.Register(api, huma.Operation{
		OperationID: "listLanguages", Method: http.MethodGet, Path: "/api/v1/languages",
		Summary:   "List active content languages",
		Responses: problemResponses(api, 400, 503), Middlewares: huma.Middlewares{publicQuery(api)},
	}, func(ctx context.Context, _ *struct{}) (*languagesOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		languages, err := reader.ListLanguages(ctx)
		if err != nil {
			return nil, publishingProblem(ctx, logger, err)
		}
		out := &languagesOutput{Body: make([]languageDTO, 0, len(languages))}
		for _, value := range languages {
			out.Body = append(out.Body, languageDTO{Code: value.Code, Name: value.Name, NativeName: value.NativeName, Direction: value.Direction})
		}
		return out, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listArticles", Method: http.MethodGet, Path: "/api/v1/articles",
		Summary:     "List published articles in one content language",
		Description: "Ordered by publication time descending, then article ID descending. An active language with no articles returns an empty array. No language fallback.",
		Responses:   problemResponses(api, 400, 404, 503), Middlewares: huma.Middlewares{publicQuery(api, languageQueryKey, limitQueryKey, offsetQueryKey)},
	}, func(ctx context.Context, input *articleListInput) (*articlesOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		articles, err := reader.ListArticles(ctx, input.Language, publishing.Page{Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return nil, publishingProblem(ctx, logger, err)
		}
		out := &articlesOutput{Body: make([]articleDTO, 0, len(articles))}
		for _, article := range articles {
			out.Body = append(out.Body, publicArticle(article))
		}
		return out, nil
	})
	responses := problemResponses(api, 400, 404, 503)
	responses["301"] = &huma.Response{
		Description: "Historical slug; Location points directly to the current address in the same content language.",
		Headers:     map[string]*huma.Param{locationHeader: {Required: true, Schema: &huma.Schema{Type: "string"}}},
		Content:     map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: huma.TypeNull}}},
	}
	huma.Register(api, huma.Operation{
		OperationID: "getArticle", Method: http.MethodGet, Path: "/api/v1/articles/{slug}",
		Summary:     "Get a published article or its canonical address",
		Description: "Missing, unpublished, disabled or untranslated content returns 404. No language fallback.",
		Responses:   responses, Middlewares: huma.Middlewares{publicQuery(api, languageQueryKey)},
	}, func(ctx context.Context, input *articleInput) (*articleOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resolved, err := reader.ResolveArticle(ctx, input.Language, input.Slug)
		if err != nil {
			return nil, publishingProblem(ctx, logger, err)
		}
		if resolved.Redirect {
			return &articleOutput{Status: http.StatusMovedPermanently, Location: "/api/v1/articles/" + url.PathEscape(resolved.Article.Slug) + "?" + url.Values{languageQueryKey: {resolved.Article.LanguageCode}}.Encode()}, nil
		}
		value := resolved.Article
		article := articleDetailDTO{ArticleID: value.ArticleID, LanguageCode: value.LanguageCode, Title: value.Title, Slug: value.Slug, BodyMarkdown: value.BodyMarkdown, PublishedTranslations: make([]publishedTranslationDTO, 0, len(resolved.PublishedTranslations))}
		for _, value := range resolved.PublishedTranslations {
			article.PublishedTranslations = append(article.PublishedTranslations, publishedTranslationDTO{LanguageCode: value.LanguageCode, Slug: value.Slug})
		}
		return &articleOutput{Status: http.StatusOK, Body: &article}, nil
	})
}

func publicArticle(value publishing.PublicArticle) articleDTO {
	return articleDTO{ArticleID: value.ArticleID, LanguageCode: value.LanguageCode, Title: value.Title, Slug: value.Slug, BodyMarkdown: value.BodyMarkdown}
}

// Huma разбирает параметры после middleware. Отклоняем неоднозначный query до
// этого шага, чтобы ошибочный ввод всегда давал безопасный 400, а не Huma 422.
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
	return validPageParameter(query, limitQueryKey, 1, 100) && validPageParameter(query, offsetQueryKey, 0, math.MaxInt)
}

func validPageParameter(query url.Values, name string, minimum, maximum int) bool {
	if !query.Has(name) {
		return true
	}
	value, err := strconv.Atoi(query.Get(name))
	return err == nil && value >= minimum && value <= maximum
}

func publishingProblem(ctx context.Context, logger *slog.Logger, err error) error {
	var typed *publishing.Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case publishing.Validation:
			return newProblem(http.StatusBadRequest, typed.Code)
		case publishing.NotFound:
			return newProblem(http.StatusNotFound, typed.Code)
		}
	}
	logDependency(ctx, logger, err)
	return newProblem(http.StatusServiceUnavailable, databaseUnavailableCode)
}

func logDependency(ctx context.Context, logger *slog.Logger, err error) {
	code, sqlState := databaseUnavailableCode, ""
	var typed *publishing.Error
	if errors.As(err, &typed) {
		code = typed.Code
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		sqlState = state.SQLState()
	}
	logger.WarnContext(ctx, "dependency_unavailable", requestIDField, ctx.Value(requestIDKey{}), "code", code, "sql_state", sqlState)
}
