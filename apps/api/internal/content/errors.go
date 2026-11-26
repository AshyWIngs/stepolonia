package content

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Registry conflicts and publication-state failures.
const (
	schemaConflictCode     = "schema_conflict"
	versionConflictCode    = "version_conflict"
	notReadyCode           = "not_ready"
	routeConflictCode      = "route_conflict"
	incompatibleSchemaCode = "incompatible_schema"
)

// Field, schema and public-projection validation failures.
const (
	rendererDependencyCode      = "renderer_dependency"
	invalidFieldCode            = "invalid_field"
	invalidScopeCode            = "invalid_scope"
	invalidRelationCode         = "invalid_relation"
	relationEditDeniedCode      = "relation_edit_denied"
	invalidLanguageCode         = "invalid_language"
	invalidValueCode            = "invalid_value"
	invalidOptionsCode          = "invalid_options"
	invalidEnumCode             = "invalid_enum"
	invalidLengthCode           = "invalid_length"
	invalidRangeCode            = "invalid_range"
	invalidDecimalCode          = "invalid_decimal"
	invalidSlugCode             = "invalid_slug"
	invalidLabelsCode           = "invalid_labels"
	invalidPublicProjectionCode = "invalid_public_projection"
)

type ErrorKind string

const (
	Validation  ErrorKind = "validation"
	Forbidden   ErrorKind = "forbidden"
	NotFound    ErrorKind = "not_found"
	Conflict    ErrorKind = "conflict"
	Unavailable ErrorKind = "unavailable"
)

type Error struct {
	Kind ErrorKind
	Code string
}

func (e *Error) Error() string                  { return e.Code }
func problem(kind ErrorKind, code string) error { return &Error{kind, code} }
func databaseError(err error) error {
	if err == nil {
		return nil
	}
	var domain *Error
	if errors.As(err, &domain) {
		return err
	}
	if conflict := transactionConflict(err); conflict != nil {
		return conflict
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "22P02", "23514", "23503":
			return problem(Validation, "invalid_value")
		case "23505":
			return problem(Conflict, routeConflictCode)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return problem(NotFound, "not_found")
	}
	return fmt.Errorf("%w: %w", problem(Unavailable, "dependency_unavailable"), err)
}

func transactionConflict(err error) *Error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "40001", "40P01", "55P03":
			return &Error{Kind: Conflict, Code: versionConflictCode}
		}
	}
	return nil
}
