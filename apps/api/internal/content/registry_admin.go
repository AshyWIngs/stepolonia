package content

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"stepolonia/apps/api/internal/identity"
)

type CollectionRenderer struct {
	Key       string       `json:"type_key"`
	Namespace string       `json:"route_namespace"`
	Available bool         `json:"available"`
	Fields    []Definition `json:"-"`
}

type CollectionSummary struct {
	TypeID      string `json:"type_id" format:"uuid"`
	TypeKey     string `json:"type_key"`
	DisplayName string `json:"display_name"`
	IconSVG     string `json:"icon_svg"`
	Enabled     bool   `json:"enabled"`
}

type ManagedCollectionCatalog struct {
	Renderers   []CollectionRenderer
	Collections []CollectionSummary
}

// ReadCollectionRenderers returns the templates and persisted collection
// identities needed to manage the installation's structure.
func (e *DraftEditor) ReadCollectionRenderers(ctx context.Context, req identity.AuthRequest) (ManagedCollectionCatalog, error) {
	if e == nil || e.auth == nil {
		return ManagedCollectionCatalog{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	catalog := ManagedCollectionCatalog{
		Renderers:   slices.Clone(rendererChoices[:]),
		Collections: []CollectionSummary{},
	}
	err := e.auth.WithCollectionRead(ctx, req, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id::text,key,display_name,icon_svg,enabled,route_namespace
 FROM public.content_types ORDER BY key`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		keys := map[string]bool{}
		namespaces := map[string]bool{}
		for rows.Next() {
			var item CollectionSummary
			var namespace string
			if err := rows.Scan(&item.TypeID, &item.TypeKey, &item.DisplayName, &item.IconSVG, &item.Enabled, &namespace); err != nil {
				return err
			}
			catalog.Collections = append(catalog.Collections, item)
			keys[item.TypeKey] = true
			namespaces[namespace] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range catalog.Renderers {
			choice := &catalog.Renderers[i]
			choice.Available = !keys[choice.Key] && !namespaces[choice.Namespace]
			choice.Fields = CollectionTemplate(choice.Namespace).Fields
		}
		return nil
	})
	if err != nil {
		return ManagedCollectionCatalog{}, err
	}
	generic := CollectionTemplate(collectionTemplate)
	generic.Available = true
	catalog.Renderers = append(catalog.Renderers, generic)
	return catalog, nil
}

func CollectionTemplate(namespace string) CollectionRenderer {
	key := rendererTypeKey(namespace)
	if key == "" && namespace != collectionTemplate {
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

type RegistryWriteResult struct {
	Schema TypeSchema
	Access identity.CollectionAccess
}

type CollectionCreate struct {
	Key, Namespace string
	DisplayName    string
	IconSVG        string
	Fields         []Definition
	InitialAccess  bool
}

const MaxCollectionNameLength = 120

func (e *DraftEditor) CreateCollection(ctx context.Context, req identity.AuthRequest, input CollectionCreate) (RegistryWriteResult, error) {
	if e == nil || e.auth == nil {
		return RegistryWriteResult{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out TypeSchema
	access, err := e.auth.WithCollectionCreate(ctx, req, input.InitialAccess, func(ctx context.Context, tx *sql.Tx) (identity.CollectionMutation, error) {
		key, displayName, err := collectionCreateMetadata(input)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		if err := rejectRegistryFieldIDs(input.Fields); err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		icon, err := normalizeCollectionIcon(input.IconSVG)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		metadata := collectionMetadata{displayName: displayName, iconSVG: icon}
		out, err = createType(ctx, tx, TypeInput{Key: key, RouteNamespace: input.Namespace, Fields: input.Fields}, metadata)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(registryWriteError(err))
		}
		return collectionChange(out, TypeSchema{}), nil
	})
	schema, err := registryCommandResult(out, err)
	return RegistryWriteResult{Schema: schema, Access: access}, err
}

func collectionCreateMetadata(input CollectionCreate) (string, string, error) {
	key := rendererTypeKey(input.Namespace)
	name := strings.TrimSpace(input.DisplayName)
	if key == "" || input.Key != "" && input.Key != key {
		return "", "", identity.ErrValidation
	}
	if strings.HasPrefix(input.Namespace, collectionNamespacePrefix) && (input.Key == "" || name == "") {
		return "", "", identity.ErrValidation
	}
	if utf8.RuneCountInString(name) > MaxCollectionNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return "", "", identity.ErrValidation
	}
	return key, name, nil
}

func (e *DraftEditor) SetCollectionAvailability(ctx context.Context, req identity.AuthRequest, typeKey string, version int64, enabled bool) (TypeSchema, error) {
	if e == nil || e.auth == nil {
		return TypeSchema{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out TypeSchema
	err := e.auth.WithCollectionAvailability(ctx, req, enabled, func(ctx context.Context, tx *sql.Tx) (string, error) {
		current, err := loadManagedCollection(ctx, tx, typeKey)
		if err != nil {
			return "", auditedCandidateError(err)
		}
		if current.Version != version {
			return "", identity.ErrVersionConflict
		}
		out = current
		if out.Enabled != enabled {
			err := tx.QueryRowContext(ctx, `UPDATE public.content_types SET enabled=$2,version=version+1
 WHERE id=$1 RETURNING version`, out.TypeID, enabled).Scan(&out.Version)
			if err != nil {
				return "", err
			}
			out.Enabled = enabled
		}
		return out.TypeID, nil
	})
	return registryCommandResult(out, err)
}

type CollectionRevision struct {
	TypeKey         string
	ExpectedVersion int64
	Fields          []Definition
	Initialize      bool
	IconSVG         *string
}

func (e *DraftEditor) ReviseCollection(ctx context.Context, req identity.AuthRequest, input CollectionRevision) (RegistryWriteResult, error) {
	if e == nil || e.auth == nil {
		return RegistryWriteResult{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out TypeSchema
	access, err := e.auth.WithCollectionRevision(ctx, req, input.Initialize, func(ctx context.Context, tx *sql.Tx) (identity.CollectionMutation, error) {
		current, err := loadManagedCollection(ctx, tx, input.TypeKey)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		if current.Version != input.ExpectedVersion {
			return identity.CollectionMutation{}, auditedCandidateError(problem(Conflict, versionConflictCode))
		}
		if input.IconSVG != nil {
			current.IconSVG, err = normalizeCollectionIcon(*input.IconSVG)
			if err != nil {
				return identity.CollectionMutation{}, auditedCandidateError(err)
			}
		}
		if err := rejectRegistryFieldIDs(input.Fields); err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		out, err = reviseType(ctx, tx, current, TypeInput{Key: current.Key, RouteNamespace: current.RouteNamespace, Fields: input.Fields})
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(registryWriteError(err))
		}
		return collectionChange(out, current), nil
	})
	schema, err := registryCommandResult(out, err)
	return RegistryWriteResult{Schema: schema, Access: access}, err
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
