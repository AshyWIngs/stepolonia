package platform

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

const (
	invalidParametersCode   = "invalid_parameters"
	databaseUnavailableCode = "database_unavailable"
)

type publicProblem struct {
	Type   string `json:"type" doc:"Problem type URI. Currently about:blank." example:"about:blank"`
	Title  string `json:"title" doc:"HTTP reason phrase for status." example:"Bad Request"`
	Status int    `json:"status" doc:"HTTP response status code." example:"400"`
	Detail string `json:"detail" doc:"Safe human-readable explanation without dependency details." example:"The request parameters are invalid."`
	Code   string `json:"code" doc:"Machine-readable failure code. See the operation response for supported codes." example:"invalid_parameters"`
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
		code := invalidParametersCode
		description := "Invalid, missing, repeated, empty or unsupported query parameters, malformed language, type key, slug or pagination. Codes include invalid_parameters, invalid_language, invalid_type_key, invalid_slug, invalid_page and invalid_value."
		switch status {
		case 404:
			code, description = "content_not_found", "The collection, language or published translation is unavailable. Code content_not_found."
		case 409:
			code, description = "version_conflict", "A database serialization, deadlock or lock conflict interrupted the read. Codes version_conflict and route_conflict. Retry the request."
		case 503:
			code, description = databaseUnavailableCode, "A dependency or public projection is unavailable. Code database_unavailable. Retry after service recovery."
		}
		result[strconv.Itoa(status)] = &huma.Response{Description: description, Content: map[string]*huma.MediaType{problemJSONMediaType: {Schema: schema, Example: newProblem(status, code)}}}
	}
	return result
}

// TransformSchema documents the stable public error envelope.
func (publicProblem) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "Public application/problem+json failure. status matches the HTTP status, code is machine-readable, and detail contains a safe generic explanation. All properties are present."
	return schema
}
