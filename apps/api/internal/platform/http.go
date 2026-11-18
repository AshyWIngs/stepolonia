package platform

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	contentTypeHeader    = "Content-Type"
	jsonMediaType        = "application/json"
	problemJSONMediaType = "application/problem+json"
)

type Pinger interface {
	Ping(context.Context) error
}

type healthOutput struct {
	Body struct {
		Status  string `json:"status" doc:"Probe outcome; ok means this probe succeeded." example:"ok"`
		Service string `json:"service" doc:"Stable identifier of the responding API service." example:"sp-api"`
	}
}

func healthy() *healthOutput {
	out := &healthOutput{}
	out.Body.Status = "ok"
	out.Body.Service = "sp-api"
	return out
}

const requestIDField = "request_id"

const requestIDHeader = "X-Request-ID"

type requestIDKey struct{}

func Handler(db Pinger, reader *content.PublicReader, logger *slog.Logger, site ...*siteconfig.Reader) http.Handler {
	return HandlerWithAuth(db, reader, logger, nil, site...)
}

// HandlerWithAuth mounts an independently configured private authentication boundary.
// Authentication routes never enter the public OpenAPI/SDK contract.
func HandlerWithAuth(db Pinger, reader *content.PublicReader, logger *slog.Logger, auth http.Handler, site ...*siteconfig.Reader) http.Handler {
	return handlerWithDocumentation(db, reader, logger, auth, false, site...)
}

// HandlerWithDocumentation enables same-origin development documentation.
func HandlerWithDocumentation(db Pinger, reader *content.PublicReader, logger *slog.Logger, auth http.Handler, site ...*siteconfig.Reader) http.Handler {
	return handlerWithDocumentation(db, reader, logger, auth, true, site...)
}

func handlerWithDocumentation(db Pinger, reader *content.PublicReader, logger *slog.Logger, auth http.Handler, docs bool, site ...*siteconfig.Reader) http.Handler {
	mux, api := newAPI(db, reader, logger, site...)
	if auth != nil {
		mux.Handle("/api/v1/admin/auth/", auth)
		mux.Handle("/api/v1/admin/users", auth)
		mux.Handle("/api/v1/admin/users/", auth)
		mux.Handle("/api/v1/admin/roles", auth)
		mux.Handle("/api/v1/admin/roles/", auth)
		mux.Handle("/api/v1/admin/content/{type_key}/{item_id}/assignees", auth)
		mux.Handle("/api/v1/admin/content/{type_key}/{item_id}", auth)
	}
	if docs {
		registerDocumentation(mux, api, auth != nil)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := rand.Text()
		w.Header().Set(requestIDHeader, requestID)
		w.Header().Set("Cache-Control", "no-store")
		response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))
		mux.ServeHTTP(response, r)
		logger.Info("http_request", requestIDField, response.Header().Get(requestIDHeader), "method", r.Method,
			"path", r.URL.Path, "status", response.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func newAPI(db Pinger, reader *content.PublicReader, logger *slog.Logger, site ...*siteconfig.Reader) (*http.ServeMux, huma.API) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Stepolonia API", "0.0.0")
	// Контракт генерируется из операций вне HTTP; служебные маршруты закрыты.
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.CreateHooks = nil
	cfg.Tags = []*huma.Tag{
		{Name: healthTag, Description: "Process liveness and database connectivity probes."},
		{Name: sitePublicationTag, Description: "Atomic published site settings and UI message catalogs."},
		{Name: contentLanguagesTag, Description: "Active languages available for published content."},
		{Name: publishedContentTag, Description: "Public collection items and canonical content addresses with private fields excluded."},
	}
	api := humago.New(mux, cfg)
	huma.Register(api, huma.Operation{
		Tags: []string{healthTag}, OperationID: "liveness", Method: http.MethodGet, Path: "/health/live",
		Summary:     "Check whether the API process is responding",
		Description: "Returns 200 without querying PostgreSQL. This probe does not establish database readiness or the presence of published content. Use the readiness probe before routing application traffic.",
	}, func(context.Context, *struct{}) (*healthOutput, error) {
		return healthy(), nil
	})
	huma.Register(api, huma.Operation{
		Tags: []string{healthTag}, OperationID: "readiness", Method: http.MethodGet, Path: "/health/ready",
		Summary:     "Check whether the API can reach PostgreSQL",
		Description: "Pings the configured runtime database with a one-second deadline. Returns 503 when the database is unavailable; a successful probe does not establish that site settings or content have been published.",
		Responses:   problemResponses(api, http.StatusServiceUnavailable),
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			logDependency(ctx, logger, err)
			return nil, newProblem(http.StatusServiceUnavailable, databaseUnavailableCode)
		}
		return healthy(), nil
	})
	registerContent(api, reader, logger)
	var siteReader *siteconfig.Reader
	if len(site) > 0 {
		siteReader = site[0]
	}
	registerSite(api, siteReader, logger)
	documentHealth(api)
	documentContentExamples(api)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHeader, problemJSONMediaType)
		w.WriteHeader(http.StatusNotFound)
		_ = api.Marshal(w, problemJSONMediaType, newProblem(http.StatusNotFound, "resource_not_found"))
	})
	return mux, api
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
