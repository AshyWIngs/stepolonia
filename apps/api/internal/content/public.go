package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PublicReader reads only security-barrier views through a dedicated runtime pool.
type PublicReader struct{ db *sql.DB }

func NewPublicReader(db *sql.DB) *PublicReader { return &PublicReader{db: db} }
func (r *PublicReader) begin(ctx context.Context) (*sql.Tx, error) {
	if r == nil || r.db == nil {
		return nil, problem(Unavailable, "database_unavailable")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, databaseError(err)
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT current_user=session_user
 AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT pg_catalog.has_schema_privilege(r.oid,'public','CREATE')
 AND NOT pg_catalog.has_database_privilege(r.oid,current_database(),'CREATE,TEMP')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relkind IN ('r','p') AND
 (pg_catalog.has_table_privilege(r.oid,c.oid,'SELECT') OR pg_catalog.has_any_column_privilege(r.oid,c.oid,'SELECT')))
 FROM pg_catalog.pg_roles r WHERE rolname=current_user`).Scan(&allowed)
	if err != nil || !allowed {
		_ = tx.Rollback()
		if err != nil {
			return nil, databaseError(err)
		}
		return nil, problem(Unavailable, "public_connection_required")
	}
	return tx, nil
}
func (r *PublicReader) ListLanguages(ctx context.Context) ([]Language, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT code,name,native_name,direction FROM public.public_languages ORDER BY code COLLATE "C"`)
	if err != nil {
		return nil, databaseError(err)
	}
	result := []Language{}
	for rows.Next() {
		var l Language
		if err = rows.Scan(&l.Code, &l.Name, &l.NativeName, &l.Direction); err != nil {
			_ = rows.Close()
			return nil, databaseError(err)
		}
		result = append(result, l)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, databaseError(err)
	}
	return result, databaseError(tx.Commit())
}
func publicPath(language, namespace, slug string) string {
	return "/" + url.PathEscape(language) + "/" + url.PathEscape(namespace) + "/" + url.PathEscape(slug)
}
func validatePublicInput(typeKey, language string) (string, error) {
	if !typeKeyPattern.MatchString(typeKey) {
		return "", problem(Validation, "invalid_type_key")
	}
	return normalizeLanguage(language)
}
func (r *PublicReader) List(ctx context.Context, typeKey, language string, page Page) (PublicPage, error) {
	code, err := validatePublicInput(typeKey, language)
	if err != nil {
		return PublicPage{}, err
	}
	if page.Limit < 1 || page.Limit > 100 || page.Offset < 0 || page.Offset > 10000 {
		return PublicPage{}, problem(Validation, "invalid_page")
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return PublicPage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = requirePublicCollection(ctx, tx, code, typeKey); err != nil {
		return PublicPage{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT item_id,type_key,route_namespace,schema_no,publication_id,language_code,slug,shared_values,localized_values,field_metadata FROM public.public_content_items WHERE type_key=$1 AND language_code=$2 ORDER BY published_at DESC,item_id DESC LIMIT $3 OFFSET $4`, typeKey, code, page.Limit+1, page.Offset)
	if err != nil {
		return PublicPage{}, databaseError(err)
	}
	items, err := scanPublicItems(rows)
	if err != nil {
		return PublicPage{}, err
	}
	result := PublicPage{Items: items}
	if len(items) > page.Limit {
		result.Items = items[:page.Limit]
		next := page.Offset + page.Limit
		if next <= 10000 {
			result.NextOffset = &next
		}
	}
	for i := range result.Items {
		if err = completePublicItem(ctx, tx, &result.Items[i]); err != nil {
			return PublicPage{}, err
		}
	}
	return result, databaseError(tx.Commit())
}

func requirePublicCollection(ctx context.Context, tx *sql.Tx, code, typeKey string) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.public_languages WHERE code=$1) AND EXISTS(SELECT 1 FROM public.public_content_types WHERE key=$2)`, code, typeKey).Scan(&exists)
	if err != nil {
		return databaseError(err)
	}
	if !exists {
		return problem(NotFound, "content_not_found")
	}
	return nil
}
func scanPublicItems(rows *sql.Rows) ([]PublicItem, error) {
	defer func() { _ = rows.Close() }()
	items := []PublicItem{}
	for rows.Next() {
		item, err := scanPublicItem(rows, nil)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, databaseError(rows.Err())
}

type publicScanner interface{ Scan(...any) error }

func scanPublicItem(row publicScanner, displayMetadata *[]byte) (PublicItem, error) {
	var item PublicItem
	var namespace string
	var schema int64
	var shared, localized, metadata []byte
	destinations := []any{&item.ItemID, &item.TypeKey, &namespace, &schema, &item.PublicationID, &item.Language, &item.Slug, &shared, &localized, &metadata}
	if displayMetadata != nil {
		destinations = append(destinations, displayMetadata)
	}
	err := row.Scan(destinations...)
	if err != nil {
		return item, databaseError(err)
	}
	if !registeredNamespace(namespace) || schema < 1 {
		return item, problem(Unavailable, invalidPublicProjectionCode)
	}
	item.SchemaNo = strconv.FormatInt(schema, 10)
	item.CanonicalURL = publicPath(item.Language, namespace, item.Slug)
	item.Shared, err = tagPublicValues(shared, metadata, scopeShared)
	if err != nil {
		return item, err
	}
	item.Localized, err = tagPublicValues(localized, metadata, scopeLocalized)
	return item, err
}
func tagPublicValues(data, metadata []byte, scope string) (map[string]PublicFieldValue, error) {
	var values map[string]json.RawMessage
	var fields map[string]struct{ Kind, Scope string }
	if json.Unmarshal(data, &values) != nil || json.Unmarshal(metadata, &fields) != nil {
		return nil, problem(Unavailable, invalidPublicProjectionCode)
	}
	result := map[string]PublicFieldValue{}
	for key, value := range values {
		field, ok := fields[key]
		if !ok || field.Scope != scope {
			return nil, problem(Unavailable, invalidPublicProjectionCode)
		}
		if !validPublicScalar(field.Kind, value) {
			return nil, problem(Unavailable, invalidPublicProjectionCode)
		}
		result[key] = PublicFieldValue{Kind: field.Kind, Value: value}
	}
	return result, nil
}
func validPublicScalar(kind string, value json.RawMessage) bool {
	if string(value) == "null" {
		return false
	}
	switch kind {
	case kindBoolean:
		var b bool
		return json.Unmarshal(value, &b) == nil
	case kindString, kindText, kindMarkdown, kindEnum, kindInteger, kindDecimal, kindDatetime:
		var s string
		return json.Unmarshal(value, &s) == nil
	default:
		return false
	}
}

func (r *PublicReader) Resolve(ctx context.Context, typeKey, language, slug string) (Resolution, error) {
	code, err := validatePublicInput(typeKey, language)
	if err != nil {
		return Resolution{}, err
	}
	if len(slug) > 200 || !slugPattern.MatchString(slug) {
		return Resolution{}, problem(Validation, invalidSlugCode)
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return Resolution{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `SELECT i.item_id,i.type_key,i.route_namespace,i.schema_no,i.publication_id,i.language_code,i.slug,i.shared_values,i.localized_values,i.field_metadata,i.display_metadata FROM public.public_content_routes r JOIN public.public_content_items i ON i.item_id=r.item_id AND i.publication_id=r.publication_id AND i.language_code=r.language_code WHERE r.type_key=$1 AND r.language_code=$2 AND r.requested_slug=$3`, typeKey, code, slug)
	var displayMetadata []byte
	item, err := scanPublicItem(row, &displayMetadata)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Resolution{}, problem(NotFound, "content_not_found")
		}
		return Resolution{}, err
	}
	if err = completePublicItem(ctx, tx, &item); err != nil {
		return Resolution{}, err
	}
	item.FieldLabels, err = publicFieldLabels(displayMetadata, item)
	if err != nil {
		return Resolution{}, err
	}
	if err = tx.Commit(); err != nil {
		return Resolution{}, databaseError(err)
	}
	return Resolution{Item: item, Redirect: item.Slug != slug}, nil
}

func publicFieldLabels(metadata []byte, item PublicItem) (map[string]string, error) {
	var fields map[string]map[string]struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal(metadata, &fields); err != nil {
		return nil, problem(Unavailable, invalidPublicProjectionCode)
	}
	labels := make(map[string]string)
	add := func(key string) {
		if label := strings.TrimSpace(fields[key][item.Language].Label); label != "" {
			labels[key] = label
		}
	}
	for key := range item.Shared {
		add(key)
	}
	for key := range item.Localized {
		add(key)
	}
	for _, relation := range item.Relations {
		add(relation.FieldKey)
	}
	return labels, nil
}
func completePublicItem(ctx context.Context, tx *sql.Tx, item *PublicItem) error {
	item.Translations = []PublicTranslation{}
	item.Relations = []PublicRelation{}
	rows, err := tx.QueryContext(ctx, `SELECT language_code,slug,route_namespace FROM public.public_content_items WHERE item_id=$1 AND publication_id=$2 ORDER BY language_code COLLATE "C"`, item.ItemID, item.PublicationID)
	if err != nil {
		return databaseError(err)
	}
	for rows.Next() {
		var t PublicTranslation
		var namespace string
		if err = rows.Scan(&t.Language, &t.Slug, &namespace); err != nil {
			_ = rows.Close()
			return databaseError(err)
		}
		if !registeredNamespace(namespace) {
			_ = rows.Close()
			return problem(Unavailable, invalidPublicProjectionCode)
		}
		t.CanonicalURL = publicPath(t.Language, namespace, t.Slug)
		item.Translations = append(item.Translations, t)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return databaseError(err)
	}
	rows, err = tx.QueryContext(ctx, `SELECT field_key,target_item_id,target_route_namespace,target_slug FROM public.public_content_relations WHERE publication_id=$1 AND language_code=$2 ORDER BY field_key COLLATE "C"`, item.PublicationID, item.Language)
	if err != nil {
		return databaseError(err)
	}
	for rows.Next() {
		var key, id, namespace, slug string
		if err = rows.Scan(&key, &id, &namespace, &slug); err != nil {
			_ = rows.Close()
			return databaseError(err)
		}
		if !registeredNamespace(namespace) {
			_ = rows.Close()
			return problem(Unavailable, invalidPublicProjectionCode)
		}
		item.Relations = append(item.Relations, PublicRelation{FieldKey: key, Targets: []PublicTarget{{ItemID: id, CanonicalURL: publicPath(item.Language, namespace, slug)}}})
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fmt.Errorf("public relations: %w", databaseError(err))
	}
	return nil
}
