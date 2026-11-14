package main

import (
	"fmt"
	"os"

	"stepolonia/apps/api/internal/platform"
)

func main() {
	data, err := platform.OpenAPIJSON()
	if err == nil {
		_, err = fmt.Fprintf(os.Stdout, "%s\n", data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
