package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"stepolonia/apps/api/internal/cms"
)

func (s *Service) CreateType(ctx context.Context, actor Actor, input TypeInput) (TypeSchema, error) {
	var out TypeSchema
	err := s.withTx(ctx, actor, string(cms.OpContentTypesManage), "", "", nil, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = createType(ctx, tx, input)
		return err
	})
	return out, err
}
func createType(ctx context.Context, tx *sql.Tx, input TypeInput) (TypeSchema, error) {
	if err := ValidateType(input); err != nil {
		return TypeSchema{}, err
	}
	id, err := newID()
	if err != nil {
		return TypeSchema{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_types(id,key,route_namespace,kind,current_schema_no) VALUES($1,$2,$3,'collection',1)`, id, input.Key, input.RouteNamespace); err != nil {
		return TypeSchema{}, err
	}
	if err := storeSchema(ctx, tx, id, 1, input.Fields); err != nil {
		return TypeSchema{}, err
	}
	return loadSchema(ctx, tx, id, 1)
}
func storeSchema(ctx context.Context, tx *sql.Tx, typeID string, number int64, fields []Definition) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_schemas(type_id,schema_no) VALUES($1,$2)`, typeID, number); err != nil {
		return err
	}
	for _, field := range fields {
		if err := storeField(ctx, tx, typeID, number, field); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE public.content_schemas SET sealed=true WHERE type_id=$1 AND schema_no=$2`, typeID, number)
	return err
}
func storeField(ctx context.Context, tx *sql.Tx, typeID string, number int64, field Definition) error {
	if field.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		field.ID = id
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_fields(type_id,field_id,key,display_labels) VALUES($1,$2,$3,$4)`, typeID, field.ID, field.Key, defaultObject(field.DisplayLabels)); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO public.content_field_definitions(type_id,schema_no,field_id,kind,scope,visibility,required,target_type_id,config) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9)`, typeID, number, field.ID, field.Kind, field.Scope, field.Visibility, field.Required, field.TargetTypeID, defaultObject(field.Config))
	return err
}
func defaultObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
func loadSchema(ctx context.Context, tx *sql.Tx, typeID string, number int64) (TypeSchema, error) {
	var schema TypeSchema
	err := tx.QueryRowContext(ctx, `SELECT id::text,key,route_namespace,version,CASE WHEN $2::bigint=0 THEN current_schema_no ELSE $2 END FROM public.content_types WHERE id=$1`, typeID, number).Scan(&schema.TypeID, &schema.Key, &schema.RouteNamespace, &schema.Version, &schema.SchemaNo)
	if err != nil {
		return schema, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.field_id::text,f.key,d.kind,d.scope,d.visibility,d.required,COALESCE(d.target_type_id::text,''),d.config,f.display_labels FROM public.content_fields f JOIN public.content_field_definitions d USING(type_id,field_id) JOIN public.content_schemas s USING(type_id,schema_no) WHERE d.type_id=$1 AND d.schema_no=$2 AND s.sealed ORDER BY f.key`, typeID, schema.SchemaNo)
	if err != nil {
		return schema, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var f Definition
		if err := rows.Scan(&f.ID, &f.Key, &f.Kind, &f.Scope, &f.Visibility, &f.Required, &f.TargetTypeID, &f.Config, &f.DisplayLabels); err != nil {
			return schema, err
		}
		schema.Fields = append(schema.Fields, f)
	}
	return schema, rows.Err()
}
func (s *Service) GetSchema(ctx context.Context, actor Actor, typeID string, number int64) (TypeSchema, error) {
	var out TypeSchema
	err := s.withTx(ctx, actor, string(cms.OpContentTypesManage), typeID, "", nil, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = loadSchema(ctx, tx, typeID, number)
		return err
	})
	return out, err
}
func (s *Service) ReviseType(ctx context.Context, actor Actor, typeID string, expected int64, input TypeInput) (TypeSchema, error) {
	var out TypeSchema
	err := s.withTx(ctx, actor, string(cms.OpContentTypesManage), typeID, "", nil, func(ctx context.Context, tx *sql.Tx) error {
		current, err := loadSchema(ctx, tx, typeID, 0)
		if err != nil {
			return err
		}
		if expected <= 0 {
			return problem(Validation, "expected_version_required")
		}
		if current.Version != expected {
			return problem(Conflict, versionConflictCode)
		}
		out, err = reviseType(ctx, tx, current, input)
		return err
	})
	return out, err
}
func reviseType(ctx context.Context, tx *sql.Tx, current TypeSchema, input TypeInput) (TypeSchema, error) {
	if err := ValidateType(input); err != nil {
		return TypeSchema{}, err
	}
	if current.Key != input.Key || current.RouteNamespace != input.RouteNamespace {
		return TypeSchema{}, problem(Validation, "immutable_type")
	}
	fields, semantic, err := revisionFields(current, input.Fields)
	if err != nil {
		return TypeSchema{}, err
	}
	if err := updateLabels(ctx, tx, current.TypeID, fields); err != nil {
		return TypeSchema{}, err
	}
	number := current.SchemaNo
	if semantic {
		number++
		if err := storeSchema(ctx, tx, current.TypeID, number, fields); err != nil {
			return TypeSchema{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.content_types SET version=version+1,current_schema_no=$2 WHERE id=$1`, current.TypeID, number); err != nil {
		return TypeSchema{}, err
	}
	return loadSchema(ctx, tx, current.TypeID, number)
}
func revisionFields(current TypeSchema, fields []Definition) ([]Definition, bool, error) {
	old := map[string]Definition{}
	for _, field := range current.Fields {
		old[field.Key] = field
	}
	semantic := len(fields) != len(old)
	for index, field := range fields {
		before, exists := old[field.Key]
		if !exists {
			if field.Required || field.ID != "" {
				return nil, false, problem(Validation, incompatibleSchemaCode)
			}
			continue
		}
		if field.ID != "" && field.ID != before.ID {
			return nil, false, problem(Validation, incompatibleSchemaCode)
		}
		field.ID = before.ID
		fields[index] = field
		a, b := field, before
		a.DisplayLabels = nil
		b.DisplayLabels = nil
		a.Config = canonicalJSON(defaultObject(a.Config))
		b.Config = canonicalJSON(defaultObject(b.Config))
		if !reflect.DeepEqual(a, b) {
			return nil, false, problem(Validation, incompatibleSchemaCode)
		}
		delete(old, field.Key)
	}
	if len(old) > 0 {
		return nil, false, problem(Validation, incompatibleSchemaCode)
	}
	return fields, semantic, nil
}
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	out, _ := json.Marshal(value)
	return out
}
func updateLabels(ctx context.Context, tx *sql.Tx, typeID string, fields []Definition) error {
	for _, field := range fields {
		if field.ID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_fields SET display_labels=$3 WHERE type_id=$1 AND field_id=$2`, typeID, field.ID, defaultObject(field.DisplayLabels)); err != nil {
			return err
		}
	}
	return nil
}
