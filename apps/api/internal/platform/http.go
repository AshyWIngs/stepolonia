package platform

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type Database interface {
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

func Handler(db Database, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Stepolonia API", "0.0.0")
	// Contracts are generated offline in Stage 1, never exposed as public routes.
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	api := humago.New(mux, cfg)
	huma.Register(api, huma.Operation{
		OperationID: "liveness", Method: http.MethodGet, Path: "/health/live",
	}, func(context.Context, *struct{}) (*healthOutput, error) {
		return healthy(), nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "readiness", Method: http.MethodGet, Path: "/health/ready",
		Errors: []int{http.StatusServiceUnavailable},
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			return nil, huma.Error503ServiceUnavailable("Required dependency is unavailable")
		}
		return healthy(), nil
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Not Found", "status": http.StatusNotFound,
		})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := rand.Text()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Cache-Control", "no-store")
		response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(response, r)
		logger.Info("http_request", "request_id", requestID, "method", r.Method,
			"path", r.URL.Path, "status", response.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
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
