package platform

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"stepolonia/apps/api/internal/publishing"
)

type Pinger interface {
	Ping(context.Context) error
}

type healthOutput struct {
	Body struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
}

func healthy() *healthOutput {
	out := &healthOutput{}
	out.Body.Status = "ok"
	out.Body.Service = "sp-api"
	return out
}

type requestIDKey struct{}

func Handler(db Pinger, reader *publishing.PublicReader, logger *slog.Logger) http.Handler {
	mux, _ := newAPI(db, reader, logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := rand.Text()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Cache-Control", "no-store")
		response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))
		mux.ServeHTTP(response, r)
		logger.Info("http_request", "request_id", requestID, "method", r.Method,
			"path", r.URL.Path, "status", response.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func newAPI(db Pinger, reader *publishing.PublicReader, logger *slog.Logger) (*http.ServeMux, huma.API) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Stepolonia API", "0.0.0")
	// Контракт генерируется из операций вне HTTP; служебные маршруты закрыты.
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)
	huma.Register(api, huma.Operation{
		OperationID: "liveness", Method: http.MethodGet, Path: "/health/live",
	}, func(context.Context, *struct{}) (*healthOutput, error) {
		return healthy(), nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "readiness", Method: http.MethodGet, Path: "/health/ready",
		Responses: problemResponses(api, http.StatusServiceUnavailable),
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			logDependency(ctx, logger, err)
			return nil, newProblem(http.StatusServiceUnavailable, "database_unavailable")
		}
		return healthy(), nil
	})
	registerPublishing(api, reader, logger)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = api.Marshal(w, "application/problem+json", newProblem(http.StatusNotFound, "resource_not_found"))
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
