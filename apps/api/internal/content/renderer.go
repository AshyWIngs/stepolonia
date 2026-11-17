package content

const (
	articleTypeKey = "article"
	eventTypeKey   = "event"
)

const (
	articlesNamespace = "articles"
	eventsNamespace   = "events"
)

const (
	slugFieldKey  = "slug"
	titleFieldKey = "title"
)

func registeredNamespace(namespace string) bool { return rendererTypeKey(namespace) != "" }
func rendererTypeKey(namespace string) string {
	switch namespace {
	case articlesNamespace:
		return articleTypeKey
	case eventsNamespace:
		return eventTypeKey
	default:
		return ""
	}
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
