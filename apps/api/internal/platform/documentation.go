package platform

import (
	"embed"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/cmshttp"
	"stepolonia/apps/api/internal/content"
)

//go:embed docsassets/*.js docsassets/*.css docsassets/LICENSE docsassets/*.LICENSE.txt docsassets/NOTICE
var documentationAssets embed.FS

const javascriptMediaType = "application/javascript; charset=utf-8"

const documentationPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Stepolonia API</title><link rel="stylesheet" href="/docs/assets/swagger-ui.css"></head>
<body><div id="swagger-ui"></div><script src="/docs/assets/swagger-ui-bundle.js"></script>
<script src="/docs/assets/swagger-ui-standalone-preset.js"></script><script src="/docs/init.js"></script></body></html>`

func registerDocumentation(mux *http.ServeMux, api huma.API, private bool) {
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		writeSpecification(w, func() ([]byte, error) { return json.MarshalIndent(api.OpenAPI(), "", "  ") })
	})
	urls := `[{url:"/openapi.json",name:"Public API"}]`
	if private {
		mux.HandleFunc("GET /openapi-auth.json", func(w http.ResponseWriter, _ *http.Request) {
			writeSpecification(w, cmshttp.OpenAPIJSON)
		})
		urls = `[{url:"/openapi.json",name:"Public API"},{url:"/openapi-auth.json",name:"Private authentication"}]`
	}
	mux.HandleFunc("GET /docs/init.js", func(w http.ResponseWriter, _ *http.Request) {
		documentationHeaders(w)
		w.Header().Set(contentTypeHeader, javascriptMediaType)
		_, _ = w.Write([]byte(`window.ui=SwaggerUIBundle({dom_id:"#swagger-ui",urls:` + urls + `,presets:[SwaggerUIBundle.presets.apis,SwaggerUIStandalonePreset],layout:"StandaloneLayout",deepLinking:true,validatorUrl:null,queryConfigEnabled:false,persistAuthorization:false,withCredentials:true});`))
	})
	for _, asset := range []struct{ name, media string }{
		{"swagger-ui.css", "text/css; charset=utf-8"},
		{"swagger-ui-bundle.js", javascriptMediaType},
		{"swagger-ui-standalone-preset.js", javascriptMediaType},
	} {
		mux.HandleFunc("GET /docs/assets/"+asset.name, func(w http.ResponseWriter, _ *http.Request) {
			documentationHeaders(w)
			w.Header().Set(contentTypeHeader, asset.media)
			data, err := documentationAssets.ReadFile("docsassets/" + asset.name)
			if err != nil {
				http.Error(w, "Documentation asset unavailable", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(data)
		})
	}
	for _, path := range []string{"/docs", "/docs/{$}"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
			documentationHeaders(w)
			w.Header().Set(contentTypeHeader, "text/html; charset=utf-8")
			_, _ = w.Write([]byte(documentationPage))
		})
	}
}

func documentationHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
}

func writeSpecification(w http.ResponseWriter, generate func() ([]byte, error)) {
	documentationHeaders(w)
	data, err := generate()
	if err != nil {
		http.Error(w, "Specification unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set(contentTypeHeader, "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func documentHealth(api huma.API) {
	for name, description := range map[string]string{
		"ErrorModel":  "A framework-generated HTTP problem for an unexpected processing failure. Public application failures use PublicProblem instead.",
		"ErrorDetail": "One structured framework error identifying a failed input location and its rejected value when available.",
	} {
		api.OpenAPI().Components.Schemas.SchemaFromRef("#/components/schemas/" + name).Description = description
	}
	api.OpenAPI().Components.Schemas.SchemaFromRef("#/components/schemas/ErrorModel").Properties["status"].Description = "HTTP response status for the failed transport operation."
	api.OpenAPI().Components.Schemas.SchemaFromRef("#/components/schemas/ErrorDetail").Properties["message"].Description = "Human-readable explanation of this individual HTTP processing error."
	for _, path := range []string{"/health/live", "/health/ready"} {
		operation := api.OpenAPI().Paths[path].Get
		response := operation.Responses["200"]
		response.Description = "The probe succeeded; inspect this operation's description for the dependencies it checks."
		media := response.Content["application/json"]
		media.Example = healthy().Body
		schema := api.OpenAPI().Components.Schemas.SchemaFromRef(media.Schema.Ref)
		schema.Description = "A successful operational probe result identifying the responding API service. Liveness and readiness check different dependencies."
		if fallback := operation.Responses["default"]; fallback != nil {
			fallback.Description = "An unexpected HTTP processing error; this is distinct from the documented database readiness failure."
			fallback.Content[problemJSONMediaType].Example = &huma.ErrorModel{
				Type: "about:blank", Title: http.StatusText(http.StatusInternalServerError), Status: http.StatusInternalServerError,
				Detail: "An unexpected HTTP processing error occurred.",
			}
		}
	}
	response := api.OpenAPI().Paths["/health/ready"].Get.Responses["503"]
	response.Description = "The database ping failed or exceeded its deadline; this API instance is not ready."
	response.Content[problemJSONMediaType].Example = newProblem(http.StatusServiceUnavailable, databaseUnavailableCode)
}

func documentContentExamples(api huma.API) {
	article, event := apiexamples.Article(), apiexamples.Event()
	for _, parameter := range api.OpenAPI().Paths["/api/v1/content/{type_key}/{slug}"].Get.Parameters {
		if parameter.Name == "slug" {
			parameter.Schema.Examples = []any{article.Slug}
		}
	}
	for _, example := range []any{
		article,
		content.PublicPage{Items: []content.PublicItem{article}},
		article.Translations[0],
		event.Relations[0],
		event.Relations[0].Targets[0],
	} {
		typeOf := reflect.TypeOf(example)
		registry := api.OpenAPI().Components.Schemas
		schema := registry.Schema(typeOf, true, typeOf.Name())
		registry.SchemaFromRef(schema.Ref).Examples = []any{example}
	}
}
