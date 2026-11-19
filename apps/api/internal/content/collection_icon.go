package content

import "stepolonia/apps/api/internal/staticsvg"

const (
	MaxCollectionIconBytes    = staticsvg.MaxBytes
	InvalidCollectionIconCode = "invalid_collection_icon"
)

func normalizeCollectionIcon(source string) (string, error) {
	icon, err := staticsvg.Normalize(source)
	if err != nil {
		return "", problem(Validation, InvalidCollectionIconCode)
	}
	return icon, nil
}
