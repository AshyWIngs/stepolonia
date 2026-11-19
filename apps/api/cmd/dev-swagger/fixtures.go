package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"stepolonia/apps/api/internal/apiexamples"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/siteconfig"
)

// seedContent writes shared synthetic examples through the real migration constraints.
// It runs once as bootstrap owner before any HTTP server or runtime pool exists.
func seedContent(ctx context.Context, db *sql.DB, token string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var owned bool
	if err = tx.QueryRowContext(ctx, `SELECT datname=$1 AND pg_catalog.shobj_description(oid,'pg_database')=$2 AND current_user=pg_catalog.pg_get_userbyid(datdba) FROM pg_catalog.pg_database WHERE datname=current_database()`, "swagger_"+token, "stepolonia:swagger:"+token).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return errors.New("refusing fixture writes outside owned disposable database")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.languages SET is_enabled=true WHERE code IN (SELECT language_code FROM public.initial_ui_messages)`); err != nil {
		return err
	}
	article, event := apiexamples.Article(), apiexamples.Event()
	var articleType, articleNamespace string
	if err = tx.QueryRowContext(ctx, `SELECT id::text,route_namespace FROM public.content_types WHERE key=$1`, article.TypeKey).Scan(&articleType, &articleNamespace); err != nil {
		return err
	}
	schema := apiexamples.EventSchema(articleType)
	if err = seedEventSchema(ctx, tx, schema); err != nil {
		return err
	}
	for _, fixture := range []publicationFixture{
		{typeID: articleType, namespace: articleNamespace, item: article},
		{typeID: articleType, namespace: articleNamespace, item: apiexamples.SecondArticle()},
		{typeID: schema.TypeID, namespace: schema.RouteNamespace, item: event},
	} {
		if err = insertPublication(ctx, tx, fixture); err != nil {
			return err
		}
	}
	if err = seedEventRelations(ctx, tx, event, schema); err != nil {
		return err
	}
	// Children must be inserted before a snapshot is sealed.
	if _, err = tx.ExecContext(ctx, `UPDATE public.content_publications SET sealed=true`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.content_items i SET published_id=p.id FROM public.content_publications p WHERE p.item_id=i.id`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.content_routes(namespace,locale_code,slug,type_id,item_id) VALUES($1,$2,'welcome-old',$3,$4)`, articleNamespace, article.Language, articleType, article.ItemID); err != nil {
		return err
	}
	return tx.Commit()
}

func seedEventSchema(ctx context.Context, tx *sql.Tx, schema content.TypeSchema) error {
	if err := content.ValidateType(content.TypeInput{Key: schema.Key, RouteNamespace: schema.RouteNamespace, Fields: schema.Fields}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_types(id,key,route_namespace,current_schema_no) VALUES($1,$2,$3,$4)`, schema.TypeID, schema.Key, schema.RouteNamespace, schema.SchemaNo); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,$2)`, schema.TypeID, schema.SchemaNo); err != nil {
		return err
	}
	for _, field := range schema.Fields {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_fields(type_id,field_id,key,display_labels) VALUES($1,$2,$3,coalesce($4::jsonb,'{}'::jsonb))`, schema.TypeID, field.ID, field.Key, []byte(field.DisplayLabels)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,target_type_id,config) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9)`, schema.TypeID, schema.SchemaNo, field.ID, field.Kind, field.Scope, field.Visibility, field.Required, field.TargetTypeID, []byte(field.Config)); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=$2`, schema.TypeID, schema.SchemaNo)
	return err
}

func seedEventRelations(ctx context.Context, tx *sql.Tx, event content.PublicItem, schema content.TypeSchema) error {
	fields := make(map[string]content.Definition, len(schema.Fields))
	for _, field := range schema.Fields {
		fields[field.Key] = field
	}
	for _, relation := range event.Relations {
		field := fields[relation.FieldKey]
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_publication_relations(publication_id,item_id,type_id,schema_no,field_id,locale_scope,target_type_id,target_item_id) VALUES($1,$2,$3,$4,$5,'',$6,$7)`, event.PublicationID, event.ItemID, schema.TypeID, schema.SchemaNo, field.ID, field.TargetTypeID, relation.Targets[0].ItemID); err != nil {
			return err
		}
	}
	return nil
}

type publicationFixture struct {
	typeID, namespace string
	item              content.PublicItem
}

func insertPublication(ctx context.Context, tx *sql.Tx, fixture publicationFixture) error {
	item := fixture.item
	schemaNo, err := strconv.ParseInt(item.SchemaNo, 10, 64)
	if err != nil {
		return err
	}
	shared, err := scalarJSON(item.Shared)
	if err != nil {
		return err
	}
	localized, err := scalarJSON(item.Localized)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(apiexamples.DisplayMetadata(item))
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.content_items(id,type_id) VALUES($1,$2)`, item.ItemID, fixture.typeID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.content_publications(id,item_id,type_id,schema_no,revision_no,shared_seq,shared_values,display_metadata) VALUES($1,$2,$3,$4,1,1,$5,$6)`, item.PublicationID, item.ItemID, fixture.typeID, schemaNo, shared, metadata); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.content_routes(namespace,locale_code,slug,type_id,item_id) VALUES($1,$2,$3,$4,$5)`, fixture.namespace, item.Language, item.Slug, fixture.typeID, item.ItemID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.content_publication_locales(publication_id,item_id,locale_code,scalar_values,locale_generation,slug) SELECT $1,$2,$3,$4,generation,$5 FROM public.languages WHERE code=$3`, item.PublicationID, item.ItemID, item.Language, localized, item.Slug)
	return err
}

func scalarJSON(values map[string]content.PublicFieldValue) ([]byte, error) {
	scalars := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		scalars[key] = value.Value
	}
	return json.Marshal(scalars)
}

func seedSite(ctx context.Context, db *sql.DB) error {
	// The site service enforces the actual atomic publication and complete catalog rules.
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT version FROM public.site_state WHERE singleton`).Scan(&version); err != nil {
		return err
	}
	draft, err := siteconfig.ReadInitialDraft(ctx, db)
	if err != nil {
		return err
	}
	settings := apiexamples.DemoSiteSettings()
	settings.Accent = draft.Settings.Accent
	draft.Settings = settings
	service := siteconfig.NewService(db)
	version, err = service.SaveDraft(ctx, version, draft)
	if err != nil {
		return err
	}
	_, _, err = service.Publish(ctx, version)
	return err
}
