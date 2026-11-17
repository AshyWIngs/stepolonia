package config

import (
	"fmt"
	"os"
)

// DocumentationEnabled explicitly opts into developer-facing HTTP documentation.
func DocumentationEnabled() (bool, error) {
	switch os.Getenv("API_DOCS_ENABLED") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("API_DOCS_ENABLED must be true or false")
	}
}
