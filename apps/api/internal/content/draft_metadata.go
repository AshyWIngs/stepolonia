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
	ItemID   string               `json:"item_id"`
	SchemaNo int64                `json:"schema_no,string"`
	Fields   []DraftFieldMetadata `json:"fields"`
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
		var schemaNo int64
		err = tx.QueryRowContext(ctx, `SELECT d.schema_no FROM public.content_drafts d
 JOIN public.content_schemas s ON s.type_id=d.type_id AND s.schema_no=d.schema_no AND s.sealed
 WHERE d.item_id=$1 AND d.type_id=$2`, itemID, access.TypeID).Scan(&schemaNo)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrNotFound
		}
		if err != nil {
			return err
		}
		fields, err := readGrantedDraftFields(ctx, tx, access, schemaNo)
		out = DraftMetadata{ItemID: itemID, SchemaNo: schemaNo, Fields: fields}
		return err
	})
	if err != nil {
		return DraftMetadata{}, err
	}
	return out, nil
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
