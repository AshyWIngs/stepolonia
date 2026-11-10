package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingDatabase struct{}

func (failingDatabase) Ping(context.Context) error {
	return errors.New("secret database connection details")
}

func TestHealthAndPrivateContracts(t *testing.T) {
	handler := Handler(failingDatabase{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/health/live", 200}, {"/health/ready", 503},
		{"/openapi.json", 404}, {"/docs", 404}, {"/api/v1/unknown", 404},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status: got %d, want %d", response.Code, test.status)
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
			if strings.Contains(response.Body.String(), "secret database") {
				t.Fatal("internal error leaked")
			}
		})
	}
}
