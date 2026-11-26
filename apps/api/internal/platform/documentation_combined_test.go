package platform

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type documentationContract struct {
	Paths map[string]map[string]map[string]any `json:"paths"`
}

func readDocumentationContract(t *testing.T, handler http.Handler, path string) documentationContract {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s returned %d: %s", path, w.Code, w.Body.String())
	}
	var doc documentationContract
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestCombinedDocumentationKeepsEveryOperationAndBoundary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	handler := HandlerWithDocumentation(failingDatabase{}, nil, logger, admin)
	public := readDocumentationContract(t, handler, publicSpecPath)
	private := readDocumentationContract(t, handler, privateSpecPath)
	combined := readDocumentationContract(t, handler, allOperationsSpecPath)

	if len(combined.Paths) != len(public.Paths)+len(private.Paths) {
		t.Fatalf("combined paths: got %d, public %d + private %d", len(combined.Paths), len(public.Paths), len(private.Paths))
	}
	for _, doc := range []documentationContract{public, private} {
		for path, operations := range doc.Paths {
			if !reflect.DeepEqual(combined.Paths[path], operations) {
				t.Errorf("combined operation metadata changed at %s", path)
			}
		}
	}
	for path, operations := range private.Paths {
		for method := range operations {
			w := httptest.NewRecorder()
			concrete := strings.NewReplacer("{id}", "00000000-0000-4000-8000-000000000001", "{item_id}", "00000000-0000-4000-8000-000000000002", "{type_key}", "article").Replace(path)
			handler.ServeHTTP(w, httptest.NewRequest(strings.ToUpper(method), concrete, nil))
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s did not reach administration: %d", method, path, w.Code)
			}
		}
	}
}

func TestCombinedDocumentationRejectsConflictingSharedSchema(t *testing.T) {
	public := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	private := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	public.Map()["Shared"] = &huma.Schema{Type: huma.TypeString}
	private.Map()["Shared"] = &huma.Schema{Type: huma.TypeInteger}
	if _, err := combinedComponents(&huma.Components{Schemas: public}, &huma.Components{Schemas: private}); err == nil {
		t.Fatal("combined documentation accepted incompatible definitions under the same name")
	}
}
