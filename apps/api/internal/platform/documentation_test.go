package platform

import (
	"bytes"
	"encoding/json"
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
	if err != nil || !bytes.Equal(exported, actual) || strings.Contains(string(actual), "/admin/") {
		t.Fatal("live public spec differs from SDK source or leaks private routes")
	}
}

func TestPrivateUserRoutesRequireConfiguredBoundary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	private := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	for _, enabled := range []bool{false, true} {
		var auth http.Handler
		if enabled {
			auth = private
		}
		handler := HandlerWithAuth(failingDatabase{}, nil, logger, auth)
		want := http.StatusNotFound
		if enabled {
			want = http.StatusForbidden
		}
		assertAdminRouteStatus(t, handler, want, []string{"/api/v1/admin/users", "/api/v1/admin/users/00000000-0000-4000-8000-000000000001/onboarding-token", "/api/v1/admin/users/00000000-0000-4000-8000-000000000001", "/api/v1/admin/users/00000000-0000-4000-8000-000000000001/activate", "/api/v1/admin/users/00000000-0000-4000-8000-000000000001/disable"})
		assertAdminRouteStatus(t, handler, want, []string{"/api/v1/admin/roles", "/api/v1/admin/roles/00000000-0000-4000-8000-000000000001"})
		const itemPath = "/api/v1/admin/content/article/00000000-0000-4000-8000-000000000001"
		assertAdminRouteStatus(t, handler, want, []string{"/api/v1/admin/content-types", "/api/v1/admin/content/article", itemPath, itemPath + "/assignees", itemPath + "/ready-shared", itemPath + "/ready", itemPath + "/publish", itemPath + "/schema"})
		assertAdminRouteStatus(t, handler, http.StatusNotFound, []string{itemPath + "/unpublish"})
	}
}

func assertAdminRouteStatus(t *testing.T, handler http.Handler, want int, paths []string) {
	t.Helper()
	for _, path := range paths {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if w.Code != want {
			t.Fatalf("%s: got %d want %d", path, w.Code, want)
		}
	}
}

func TestPrivateCommandRequestCorrelation(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	const serverRequestID = "10000000-0000-4000-8000-000000000001"
	private := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(requestIDHeader, serverRequestID)
		w.WriteHeader(http.StatusForbidden)
	})
	handler := HandlerWithAuth(failingDatabase{}, nil, logger, private)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader("{}"))
	r.Header.Set(requestIDHeader, "untrusted-client-value")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var event struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get(requestIDHeader) != serverRequestID || event.RequestID != serverRequestID {
		t.Fatal("response and command log must use the server audit correlation ID")
	}
}
