package platform

import (
	"encoding/json"
	"io"
	"log/slog"
)

// OpenAPIJSON uses the same operation registration as Handler.
// Generation runs without requests, database connections, or an HTTP listener.
func OpenAPIJSON() ([]byte, error) {
	_, api := newAPI(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return json.MarshalIndent(api.OpenAPI(), "", "  ")
}
