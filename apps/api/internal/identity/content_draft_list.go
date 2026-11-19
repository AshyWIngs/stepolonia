package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const (
	DefaultDraftPageLimit = 20
	MaxDraftPageLimit     = 100
	MaxDraftPageOffset    = 10000
)

// MaxDraftSummaryTitleChars bounds the display text returned per draft locale.
const MaxDraftSummaryTitleChars = 256

type DraftSummary struct {
	ItemID           string            `json:"item_id"`
	TypeID           string            `json:"type_id"`
	SchemaNo         int64             `json:"schema_no,string"`
	Version          int64             `json:"version,string"`
	Titles           map[string]string `json:"titles,omitempty"`
	PublishedLocales []string          `json:"published_locales,omitempty"`
}

type DraftPage struct {
	Items      []DraftSummary `json:"items"`
	NextOffset *int           `json:"next_offset"`
}

// DraftListPresentation carries the renderer and locale constraints owned by content.
type DraftListPresentation struct {
	RendererNamespaces []string
	TitleKey           string
	MaxLocales         int
}

// ReadDraftPage scopes items before pagination and projects only authorized titles.
func (s *AuthService) ReadDraftPage(ctx context.Context, req AuthRequest, typeKey string, limit, offset int, presentation DraftListPresentation) (DraftPage, error) {
	if !validDraftPage(limit, offset) {
		return DraftPage{}, ErrValidation
	}
	var page DraftPage
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		scope, err := draftListScope(ctx, tx, session.user, typeKey)
		if err != nil {
			return err
		}
		page, err = readDraftPage(ctx, tx, session.user, scope, limit, offset, presentation)
		return err
	})
	if err != nil {
		return DraftPage{}, err
	}
	return page, nil
}

func validDraftPage(limit, offset int) bool {
	return limit >= 1 && limit <= MaxDraftPageLimit && offset >= 0 && offset <= MaxDraftPageOffset
}

func nextDraftPageOffset(limit, offset int) *int {
	next := offset + limit
	if next > MaxDraftPageOffset {
		return nil
	}
	return &next
}

type draftPageScope struct {
	typeID  string
	anyItem bool
}

func draftListScope(ctx context.Context, tx *sql.Tx, userID, typeKey string) (draftPageScope, error) {
	var scope draftPageScope
	err := tx.QueryRowContext(ctx, `SELECT t.id::text, bool_or(g.object_scope='any')
 FROM public.content_types t
 JOIN public.role_grants g ON g.type_id=t.id
 JOIN public.permission_operations o ON o.key=g.operation AND o.scope='content'
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$1
 WHERE t.key=$2 AND g.operation=$3 AND g.scope='content' AND g.object_scope IN ('any','assigned')
 GROUP BY t.id`, userID, typeKey, OpContentRead).Scan(&scope.typeID, &scope.anyItem)
	if errors.Is(err, sql.ErrNoRows) {
		return draftPageScope{}, ErrNotFound
	}
	return scope, err
}

const draftPageQuery = `WITH page AS MATERIALIZED (
 SELECT i.id,i.type_id,d.schema_no,i.version,i.published_id,(assignment.item_id IS NOT NULL) AS assigned
 FROM public.content_items i
 JOIN public.content_drafts d ON d.item_id=i.id AND d.type_id=i.type_id
 LEFT JOIN public.content_item_assignments assignment ON assignment.item_id=i.id AND assignment.user_id=$3
 WHERE i.type_id=$1 AND ($2::boolean OR assignment.item_id IS NOT NULL)
 ORDER BY i.id LIMIT $4 OFFSET $5
 ), title_grants AS (
 SELECT f.field_id,g.object_scope
 FROM public.role_grants g
 JOIN public.permission_operations o ON o.key=g.operation AND o.scope='content'
 JOIN public.roles r ON r.id=g.role_id AND r.state='active'
 JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$3
 JOIN public.grant_fields f ON f.grant_id=g.id AND f.type_id=g.type_id
 WHERE g.type_id=$1 AND g.operation=$6 AND g.scope='content'
 )
 SELECT page.id::text,page.schema_no,page.version,titles.values,published.locales
 FROM page
 LEFT JOIN LATERAL (
   SELECT jsonb_object_agg(localized.locale_code,localized.title) AS values
   FROM (
     SELECT l.locale_code,left(btrim(l.scalar_values ->> f.key,E' \t\n\r'),$10) AS title
     FROM public.content_types t
     JOIN public.content_schemas s ON s.type_id=t.id AND s.schema_no=page.schema_no AND s.sealed
     JOIN public.content_fields f ON f.type_id=t.id AND f.key=$8
     JOIN public.content_field_definitions d ON d.type_id=t.id AND d.schema_no=s.schema_no AND d.field_id=f.field_id
     JOIN public.content_draft_locales l ON l.item_id=page.id
     WHERE t.id=page.type_id AND t.route_namespace=ANY($7::text[])
       AND d.kind='string' AND d.scope='localized' AND d.visibility='public' AND d.required
       AND EXISTS (SELECT 1 FROM title_grants g WHERE g.field_id=f.field_id
         AND (g.object_scope='any' OR (g.object_scope='assigned' AND page.assigned)))
       AND jsonb_typeof(l.scalar_values -> f.key)='string'
       AND btrim(l.scalar_values ->> f.key,E' \t\n\r') <> ''
     ORDER BY l.locale_code LIMIT $9
   ) localized
 ) titles ON true
 LEFT JOIN LATERAL (
   SELECT jsonb_agg(visible.locale_code ORDER BY visible.locale_code) AS locales
   FROM (
     SELECT l.locale_code
     FROM public.content_publications p
     JOIN public.content_publication_locales l ON l.publication_id=p.id
     JOIN public.languages lang ON lang.code=l.locale_code AND lang.is_enabled AND lang.generation=l.locale_generation
     WHERE p.id=page.published_id AND p.item_id=page.id AND p.sealed
     ORDER BY l.locale_code LIMIT $9
   ) visible
 ) published ON true
 ORDER BY page.id`

func readDraftPage(ctx context.Context, tx *sql.Tx, userID string, scope draftPageScope, limit, offset int, presentation DraftListPresentation) (DraftPage, error) {
	rows, err := tx.QueryContext(ctx, draftPageQuery, scope.typeID, scope.anyItem, userID, limit+1, offset, OpContentRead,
		presentation.RendererNamespaces, presentation.TitleKey, presentation.MaxLocales, MaxDraftSummaryTitleChars)
	if err != nil {
		return DraftPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := DraftPage{Items: make([]DraftSummary, 0, limit)}
	for rows.Next() {
		if len(page.Items) == limit {
			page.NextOffset = nextDraftPageOffset(limit, offset)
			break
		}
		var item DraftSummary
		item.TypeID = scope.typeID
		var titles, published []byte
		if err := rows.Scan(&item.ItemID, &item.SchemaNo, &item.Version, &titles, &published); err != nil {
			return DraftPage{}, err
		}
		if err := decodeDraftSummary(&item, titles, published); err != nil {
			return DraftPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return DraftPage{}, err
	}
	return page, nil
}

func decodeDraftSummary(item *DraftSummary, titles, published []byte) error {
	if len(titles) != 0 {
		if err := json.Unmarshal(titles, &item.Titles); err != nil {
			return err
		}
		for locale, title := range item.Titles {
			if title = strings.TrimSpace(title); title == "" {
				delete(item.Titles, locale)
			} else {
				item.Titles[locale] = title
			}
		}
	}
	if len(published) != 0 {
		return json.Unmarshal(published, &item.PublishedLocales)
	}
	return nil
}
