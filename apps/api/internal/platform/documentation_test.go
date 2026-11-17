package platform

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const publicSpecPath = "/openapi.json"

func TestLiveDocumentationBoundary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	private := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	for _, enabled := range []bool{false, true} {
		handler := HandlerWithAuth(failingDatabase{}, nil, logger, private)
		if enabled {
			handler = HandlerWithDocumentation(failingDatabase{}, nil, logger, private)
		}
		assertDocumentationPaths(t, handler, enabled)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/login", nil))
		if w.Code != http.StatusForbidden {
			t.Fatal("documentation bypassed private handler")
		}
	}
	handler := HandlerWithDocumentation(failingDatabase{}, nil, logger, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi-auth.json", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("private spec exposed while auth disabled")
	}
}

func assertDocumentationPaths(t *testing.T, handler http.Handler, enabled bool) {
	t.Helper()
	for _, path := range []string{"/docs", "/docs/", "/docs/init.js", "/docs/assets/swagger-ui.css", "/docs/assets/swagger-ui-bundle.js", "/docs/assets/swagger-ui-standalone-preset.js", publicSpecPath, "/openapi-auth.json"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		want := http.StatusNotFound
		if enabled {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("enabled=%t %s: got %d want %d", enabled, path, w.Code, want)
		}
		if !enabled {
			continue
		}
		if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing documentation security headers: %s", path)
		}
		if path == publicSpecPath {
			assertLivePublicSpecification(t, w.Body.Bytes())
		}
	}
}

func assertLivePublicSpecification(t *testing.T, actual []byte) {
	t.Helper()
	exported, err := OpenAPIJSON()
	if err != nil || !bytes.Equal(exported, actual) || strings.Contains(string(actual), "/admin/auth/") {
		t.Fatal("live public spec differs from SDK source or leaks private routes")
	}
}
