package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

const (
	publicationSlugLimit    = 200
	maxRouteCandidates      = 1000
	publicationSlugFallback = "item"
)

func allocatePublicationSlug(ctx context.Context, tx *sql.Tx, itemID string, schema TypeSchema, local *publicationLocale) error {
	index := slices.IndexFunc(schema.Fields, func(field Definition) bool { return field.Scope == scopeLocalized && field.Key == slugFieldKey })
	if index < 0 {
		return problem(Validation, invalidSlugCode)
	}
	config, err := configFor(schema.Fields[index])
	if err != nil {
		return err
	}
	base := publicationSlugBase(local.Slug, itemID, config)
	if err := validateText(base, config); err != nil {
		return err
	}
	route, err := findPublicationRoute(ctx, tx, schema.RouteNamespace, itemID, local.Locale, base, config)
	if err != nil {
		return err
	}
	if route == "" {
		if base != local.Slug {
			// Keep the valid legacy route if the schema leaves no readable alternative.
			return nil
		}
		return problem(Conflict, routeConflictCode)
	}
	if route == local.Slug {
		return nil
	}
	value, err := json.Marshal(route)
	if err != nil {
		return err
	}
	local.Slug = route
	local.Values = maps.Clone(local.Values)
	local.Values[slugFieldKey] = value
	return nil
}

func publicationSlugBase(slug, itemID string, config fieldConfig) string {
	base := legacySlugBase(slug, itemID)
	if config.MinLength == nil {
		return base
	}
	missing := *config.MinLength - len(base)
	if missing <= 0 {
		return base
	}
	if missing == 1 {
		return base + "1"
	}
	length := len(publicationSlugFallback)
	filler := strings.Repeat(publicationSlugFallback, (missing+length-2)/length)
	return base + "-" + filler[:missing-1]
}

func legacySlugBase(slug, itemID string) string {
	if slug == publicationSlugFallback+"-"+itemID {
		return publicationSlugFallback
	}
	return strings.TrimSuffix(slug, "-"+strings.ReplaceAll(itemID, "-", ""))
}

// The publication transaction holds the installation write mutex. Historical
// aliases stay reserved, and an item's own aliases are available for reuse.
func findPublicationRoute(ctx context.Context, tx *sql.Tx, namespace, itemID, locale, base string, config fieldConfig) (string, error) {
	limit, minimum := publicationSlugLimit, 1
	if config.MaxLength != nil {
		limit = min(limit, *config.MaxLength)
	}
	if config.MinLength != nil {
		minimum = *config.MinLength
	}
	var route sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT CASE WHEN NOT EXISTS (
    SELECT 1 FROM public.content_routes
    WHERE namespace = $1 AND locale_code = $2 AND slug = $3 AND item_id <> $4
) THEN $3::text ELSE (
    SELECT candidate.slug
    FROM (
        SELECT rtrim(left($3, $5::integer - length('-' || number::text)), '-') || '-' || number::text AS slug, number
        FROM generate_series(2, $6::integer) AS ordinal(number)
        WHERE $5::integer > length('-' || number::text)
    ) AS candidate
    WHERE length(candidate.slug) >= $7 AND NOT EXISTS (
        SELECT 1 FROM public.content_routes
        WHERE namespace = $1 AND locale_code = $2 AND slug = candidate.slug AND item_id <> $4
    )
    ORDER BY candidate.number
    LIMIT 1
) END`, namespace, locale, base, itemID, limit, maxRouteCandidates, minimum).Scan(&route)
	if err != nil {
		return "", err
	}
	return route.String, nil
}
