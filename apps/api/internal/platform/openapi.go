package platform

import (
	"encoding/json"
	"io"
	"log/slog"
)

// OpenAPIJSON строит контракт теми же регистрациями, что и Handler. Обработчики
// не вызываются: генерация не создаёт соединения, пулы или HTTP-сервер.
func OpenAPIJSON() ([]byte, error) {
	_, api := newAPI(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return json.MarshalIndent(api.OpenAPI(), "", "  ")
}
