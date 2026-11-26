package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/danielgtaylor/huma/v2"
)

func combinedOpenAPIJSON(public, private *huma.OpenAPI) ([]byte, error) {
	doc := *public
	doc.Info = &huma.Info{
		Title:       "Portal API",
		Version:     public.Info.Version,
		Description: "Public and administration operations on the current installation. Protected administration operations require the canonical HTTPS origin and the documented session and permissions.",
	}
	doc.Paths = maps.Clone(public.Paths)
	for path, item := range private.Paths {
		if _, exists := doc.Paths[path]; exists {
			return nil, fmt.Errorf("duplicate documentation path %q", path)
		}
		doc.Paths[path] = item
	}
	doc.Servers = private.Servers
	doc.Tags = append(slices.Clone(public.Tags), private.Tags...)
	components, err := combinedComponents(public.Components, private.Components)
	if err != nil {
		return nil, err
	}
	doc.Components = components
	return json.Marshal(&doc)
}

func combinedComponents(public, private *huma.Components) (*huma.Components, error) {
	components := *public
	components.SecuritySchemes = make(map[string]*huma.SecurityScheme, len(public.SecuritySchemes)+len(private.SecuritySchemes))
	maps.Copy(components.SecuritySchemes, private.SecuritySchemes)
	for name, scheme := range public.SecuritySchemes {
		if _, exists := components.SecuritySchemes[name]; exists {
			return nil, fmt.Errorf("duplicate documentation security scheme %q", name)
		}
		components.SecuritySchemes[name] = scheme
	}

	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	maps.Copy(registry.Map(), public.Schemas.Map())
	for name, schema := range private.Schemas.Map() {
		if shared, exists := registry.Map()[name]; exists {
			merged, err := sharedDocumentationSchema(name, shared, schema)
			if err != nil {
				return nil, err
			}
			schema = merged
		}
		registry.Map()[name] = schema
	}
	components.Schemas = registry
	return &components, nil
}

func sharedDocumentationSchema(name string, public, private *huma.Schema) (*huma.Schema, error) {
	publicShape, privateShape := *public, *private
	publicShape.Description, privateShape.Description = "", ""
	publicShape.Examples, privateShape.Examples = nil, nil
	first, err := json.Marshal(&publicShape)
	if err != nil {
		return nil, err
	}
	second, err := json.Marshal(&privateShape)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(first, second) {
		return nil, fmt.Errorf("incompatible public and administrative schema %q", name)
	}
	merged := *public
	merged.Examples = append(slices.Clone(public.Examples), private.Examples...)
	return &merged, nil
}
