package content

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
	"stepolonia/apps/api/internal/identity"
)

type CollectionRenderer struct {
	Key       string       `json:"type_key"`
	Namespace string       `json:"route_namespace"`
	Available bool         `json:"available"`
	Fields    []Definition `json:"-"`
}

// ReadCollectionRenderers returns the renderer choices the server knows about
// and reports which have an existing collection.
func (e *DraftEditor) ReadCollectionRenderers(ctx context.Context, req identity.AuthRequest) ([]CollectionRenderer, error) {
	if e == nil || e.auth == nil {
		return nil, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	choices := slices.Clone(rendererChoices[:])
	err := e.auth.WithCollectionRead(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		for i := range choices {
			choice := &choices[i]
			if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS (
 SELECT 1 FROM public.content_types WHERE key=$1 OR route_namespace=$2)`, choice.Key, choice.Namespace).Scan(&choice.Available); err != nil {
				return err
			}
			choice.Fields = CollectionTemplate(choice.Namespace).Fields
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return choices, nil
}

func CollectionTemplate(namespace string) CollectionRenderer {
	key := rendererTypeKey(namespace)
	if key == "" {
		return CollectionRenderer{}
	}
	fields := initialType().Fields
	if key != articleTypeKey {
		fields = slices.DeleteFunc(fields, func(field Definition) bool { return field.Key == bodyFieldKey })
	}
	return CollectionRenderer{Key: key, Namespace: namespace, Fields: fields}
}

func (e *DraftEditor) ReadCollectionForManagement(ctx context.Context, req identity.AuthRequest, typeKey string) (TypeSchema, error) {
	if e == nil || e.auth == nil {
		return TypeSchema{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	var out TypeSchema
	err := e.auth.WithCollectionRead(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = loadManagedCollection(ctx, tx, typeKey)
		return err
	})
	return registryCommandResult(out, err)
}

func (e *DraftEditor) CreateCollection(ctx context.Context, req identity.AuthRequest, namespace string, fields []Definition, initialize bool) (TypeSchema, error) {
	if e == nil || e.auth == nil {
		return TypeSchema{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out TypeSchema
	err := e.auth.WithCollectionCreate(ctx, req, initialize, func(ctx context.Context, tx *sql.Tx) (identity.CollectionMutation, error) {
		if err := rejectRegistryFieldIDs(fields); err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		var err error
		out, err = createType(ctx, tx, TypeInput{Key: rendererTypeKey(namespace), RouteNamespace: namespace, Fields: fields})
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(registryWriteError(err))
		}
		return collectionChange(out, TypeSchema{}), nil
	})
	return registryCommandResult(out, err)
}

func (e *DraftEditor) ReviseCollection(ctx context.Context, req identity.AuthRequest, typeKey string, version int64, fields []Definition, initialize bool) (TypeSchema, error) {
	if e == nil || e.auth == nil {
		return TypeSchema{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out TypeSchema
	err := e.auth.WithCollectionRevision(ctx, req, initialize, func(ctx context.Context, tx *sql.Tx) (identity.CollectionMutation, error) {
		current, err := loadManagedCollection(ctx, tx, typeKey)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		if current.Version != version {
			return identity.CollectionMutation{}, auditedCandidateError(problem(Conflict, versionConflictCode))
		}
		if err := rejectRegistryFieldIDs(fields); err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		out, err = reviseType(ctx, tx, current, TypeInput{Key: current.Key, RouteNamespace: current.RouteNamespace, Fields: fields})
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(registryWriteError(err))
		}
		return collectionChange(out, current), nil
	})
	return registryCommandResult(out, err)
}

func loadManagedCollection(ctx context.Context, tx *sql.Tx, typeKey string) (TypeSchema, error) {
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM public.content_types WHERE key=$1`, typeKey).Scan(&id); err != nil {
		return TypeSchema{}, databaseError(err)
	}
	schema, err := loadSchema(ctx, tx, id, 0)
	return schema, databaseError(err)
}

func rejectRegistryFieldIDs(fields []Definition) error {
	if slices.ContainsFunc(fields, func(field Definition) bool { return field.ID != "" }) {
		return problem(Validation, invalidFieldCode)
	}
	return nil
}

func collectionChange(after, before TypeSchema) identity.CollectionMutation {
	known := make(map[string]bool, len(before.Fields))
	for _, field := range before.Fields {
		known[field.ID] = true
	}
	change := identity.CollectionMutation{TypeID: after.TypeID, SchemaNo: after.SchemaNo, PreviousSchema: before.SchemaNo}
	for _, field := range after.Fields {
		if !known[field.ID] {
			change.NewFieldIDs = append(change.NewFieldIDs, field.ID)
		}
	}
	return change
}

func registryWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return problem(Conflict, "collection_exists")
	}
	return databaseError(err)
}

func registryCommandResult(schema TypeSchema, err error) (TypeSchema, error) {
	_, err = draftCommandResult(DraftState{}, err)
	if err != nil {
		return TypeSchema{}, err
	}
	return schema, nil
}
