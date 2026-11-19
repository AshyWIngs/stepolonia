package content

import "strings"

const (
	articleTypeKey = "article"
	eventTypeKey   = "event"
)

const (
	articlesNamespace         = "articles"
	eventsNamespace           = "events"
	collectionTemplate        = "collection"
	collectionNamespacePrefix = collectionTemplate + "-"
	maxRouteNamespaceLength   = 64
)

const (
	slugFieldKey  = "slug"
	titleFieldKey = "title"
)

func registeredNamespace(namespace string) bool { return rendererTypeKey(namespace) != "" }

var rendererChoices = [...]CollectionRenderer{
	{Key: articleTypeKey, Namespace: articlesNamespace},
	{Key: eventTypeKey, Namespace: eventsNamespace},
}

func rendererTypeKey(namespace string) string {
	for _, renderer := range rendererChoices {
		if renderer.Namespace == namespace {
			return renderer.Key
		}
	}
	remainder, generic := strings.CutPrefix(namespace, collectionNamespacePrefix)
	if generic && len(namespace) <= maxRouteNamespaceLength {
		key := strings.ReplaceAll(remainder, "-", "_")
		if typeKeyPattern.MatchString(key) && key != articleTypeKey && key != eventTypeKey && key != "renderers" &&
			namespace == collectionNamespacePrefix+strings.ReplaceAll(key, "_", "-") {
			return key
		}
	}
	return ""
}

func validateRendererFields(fields map[string]Definition) error {
	for _, key := range []string{slugFieldKey, titleFieldKey} {
		field, exists := fields[key]
		if !exists || field.Kind != kindString || field.Scope != scopeLocalized || field.Visibility != visibilityPublic || !field.Required {
			return problem(Validation, rendererDependencyCode)
		}
		config, err := configFor(field)
		if err != nil {
			return err
		}
		if key == slugFieldKey && !config.Slug {
			return problem(Validation, rendererDependencyCode)
		}
		if key == titleFieldKey && (config.MinLength == nil || *config.MinLength < 1) {
			return problem(Validation, rendererDependencyCode)
		}
	}
	return nil
}
