package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

type DraftFieldMetadata struct {
	FieldID       string          `json:"field_id" format:"uuid" doc:"Stable field ID used in grants."`
	Key           string          `json:"key" pattern:"^[a-z][a-z0-9_]{0,63}$" doc:"Stable key used in scalar draft changes."`
	Kind          string          `json:"kind" doc:"Registry kind of the field."`
	Scope         string          `json:"scope" doc:"Shared across locales or stored per locale."`
	Visibility    string          `json:"visibility" doc:"Whether the field can appear in public projections."`
	Required      bool            `json:"required" doc:"Requires a value for readiness. Drafts can remain incomplete."`
	Editable      bool            `json:"editable" doc:"The current session can update this field on the draft."`
	Config        json.RawMessage `json:"config"`
	DisplayLabels json.RawMessage `json:"display_labels"`
}

func (DraftFieldMetadata) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["kind"].Enum = []any{kindString, kindText, kindMarkdown, kindEnum, kindInteger, kindDecimal, kindDatetime, kindBoolean, kindRelation}
	schema.Properties["scope"].Enum = []any{scopeShared, scopeLocalized}
	schema.Properties["visibility"].Enum = []any{visibilityPublic, visibilityPrivate}
	schema.Properties["config"] = &huma.Schema{
		Type: huma.TypeObject, Description: "Kind-specific constraints from the pinned definition.",
	}
	schema.Properties["display_labels"] = &huma.Schema{
		Type: huma.TypeObject, Description: "Current locale-keyed labels and help text.",
		AdditionalProperties: &huma.Schema{
			Type: huma.TypeObject, AdditionalProperties: false,
			Properties: map[string]*huma.Schema{"label": {Type: huma.TypeString}, "help": {Type: huma.TypeString}},
		},
	}
	schema.AdditionalProperties = false
	return schema
}

type DraftMetadata struct {
	ItemID          string               `json:"item_id"`
	SchemaNo        int64                `json:"schema_no,string"`
	Version         int64                `json:"version,string"`
	CurrentSchemaNo int64                `json:"current_schema_no,string"`
	Fields          []DraftFieldMetadata `json:"fields"`
	Actions         DraftActions         `json:"actions"`
	Published       []PublicTranslation  `json:"published"`
}

type DraftActions struct {
	Ready     bool `json:"ready" doc:"The session holds an item-level readiness grant. The command also requires the relevant field grants."`
	Publish   bool `json:"publish" doc:"The session holds an item-level publication grant. Field grants and readiness are checked by the command."`
	Unpublish bool `json:"unpublish" doc:"The session holds a grant to remove this item's publication."`
	Adopt     bool `json:"adopt" doc:"The session can update all fields of the current collection schema."`
}

// ListReadableCollections returns the collection choices for the draft browser.
func (e *DraftEditor) ListReadableCollections(ctx context.Context, req identity.AuthRequest, limit, offset int) (identity.ReadableCollectionPage, error) {
	if e == nil || e.auth == nil {
		return identity.ReadableCollectionPage{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	return e.auth.ListReadableCollections(ctx, req, limit, offset)
}

// ReadDraftMetadata returns the readable field definitions from the pinned
// draft schema. Stable field IDs resolve the current labels and help text.
func (e *DraftEditor) ReadDraftMetadata(ctx context.Context, req identity.AuthRequest, typeKey, itemID string) (DraftMetadata, error) {
	if e == nil || e.auth == nil {
		return DraftMetadata{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	var out DraftMetadata
	err := e.auth.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session identity.Session) error {
		access, err := session.LoadContentItemAccess(ctx, tx, identity.OpContentRead, itemID)
		if err != nil {
			return err
		}
		if access.TypeKey != typeKey {
			return identity.ErrNotFound
		}
		out.ItemID = itemID
		err = tx.QueryRowContext(ctx, `SELECT d.schema_no,t.current_schema_no,i.version FROM public.content_drafts d
 JOIN public.content_types t ON t.id=d.type_id
 JOIN public.content_items i ON i.id=d.item_id AND i.type_id=d.type_id
 JOIN public.content_schemas s ON s.type_id=d.type_id AND s.schema_no=d.schema_no AND s.sealed
 WHERE d.item_id=$1 AND d.type_id=$2`, itemID, access.TypeID).Scan(&out.SchemaNo, &out.CurrentSchemaNo, &out.Version)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrNotFound
		}
		if err != nil {
			return err
		}
		out.Fields, err = readGrantedDraftFields(ctx, tx, access, out.SchemaNo)
		if err != nil {
			return err
		}
		if err := loadDraftActionHints(ctx, tx, session, access.TypeID, &out); err != nil {
			return err
		}
		out.Published, err = readDraftPublicationLinks(ctx, tx, itemID)
		return err
	})
	if err != nil {
		return DraftMetadata{}, err
	}
	return out, nil
}

func loadDraftActionHints(ctx context.Context, tx *sql.Tx, session identity.Session, typeID string, out *DraftMetadata) error {
	update, err := optionalItemAccess(ctx, tx, session, identity.OpContentUpdate, out.ItemID)
	if err != nil {
		return err
	}
	for i := range out.Fields {
		out.Fields[i].Editable = update.AllowsField(out.Fields[i].FieldID)
	}
	if out.CurrentSchemaNo != out.SchemaNo && update.TypeID != "" {
		ids := update.FieldIDs()
		if ids == nil {
			ids = []string{}
		}
		err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS (
 SELECT 1 FROM public.content_field_definitions
 WHERE type_id=$1 AND schema_no=$2 AND NOT (field_id=ANY($3::uuid[])))`,
			typeID, out.CurrentSchemaNo, ids).Scan(&out.Actions.Adopt)
		if err != nil {
			return err
		}
	}
	for _, hint := range []struct {
		op      identity.Operation
		allowed *bool
	}{
		{identity.OpContentReady, &out.Actions.Ready},
		{identity.OpContentPublish, &out.Actions.Publish},
		{identity.OpContentUnpublish, &out.Actions.Unpublish},
	} {
		access, err := optionalItemAccess(ctx, tx, session, hint.op, out.ItemID)
		if err != nil {
			return err
		}
		*hint.allowed = access.TypeID != ""
	}
	return nil
}

func optionalItemAccess(ctx context.Context, tx *sql.Tx, session identity.Session, op identity.Operation, itemID string) (identity.ContentItemAccess, error) {
	access, err := session.LoadContentItemAccess(ctx, tx, op, itemID)
	if errors.Is(err, identity.ErrNotFound) {
		return identity.ContentItemAccess{}, nil
	}
	return access, err
}

func readDraftPublicationLinks(ctx context.Context, tx *sql.Tx, itemID string) ([]PublicTranslation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.locale_code,l.slug,t.route_namespace
 FROM public.content_items i JOIN public.content_types t ON t.id=i.type_id
 JOIN public.content_publications p ON p.id=i.published_id AND p.item_id=i.id AND p.sealed
 JOIN public.content_publication_locales l ON l.publication_id=p.id
 JOIN public.languages lang ON lang.code=l.locale_code AND lang.is_enabled AND lang.generation=l.locale_generation
 WHERE i.id=$1 ORDER BY l.locale_code`, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []PublicTranslation{}
	for rows.Next() {
		var translation PublicTranslation
		var namespace string
		if err := rows.Scan(&translation.Language, &translation.Slug, &namespace); err != nil {
			return nil, err
		}
		translation.CanonicalURL = publicPath(translation.Language, namespace, translation.Slug)
		out = append(out, translation)
	}
	return out, rows.Err()
}

func readGrantedDraftFields(ctx context.Context, tx *sql.Tx, access identity.ContentItemAccess, schemaNo int64) ([]DraftFieldMetadata, error) {
	fields := []DraftFieldMetadata{}
	ids := access.FieldIDs()
	if len(ids) == 0 {
		return fields, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.field_id::text,f.key,d.kind,d.scope,d.visibility,d.required,d.config,f.display_labels
 FROM public.content_field_definitions d JOIN public.content_fields f USING(type_id,field_id)
 WHERE d.type_id=$1 AND d.schema_no=$2 AND d.field_id=ANY($3::uuid[])
 ORDER BY f.key`, access.TypeID, schemaNo, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var field DraftFieldMetadata
		if err := rows.Scan(&field.FieldID, &field.Key, &field.Kind, &field.Scope, &field.Visibility,
			&field.Required, &field.Config, &field.DisplayLabels); err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}
