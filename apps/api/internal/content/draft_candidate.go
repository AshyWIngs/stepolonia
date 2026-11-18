package content

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"stepolonia/apps/api/internal/cms"
)

// Leave time to write the response within the HTTP server deadline.
const draftCandidateTimeout = 8 * time.Second

type draftCandidateTarget struct {
	typeKey, itemID string
	version, schema int64
}

type draftCandidateCommand func(context.Context, *sql.Tx, *DraftState, TypeSchema, relationAccess) error

// MarkSharedReadyForType records readiness for the draft's shared fields.
func (e *DraftEditor) MarkSharedReadyForType(ctx context.Context, req cms.AuthRequest, typeKey, itemID string, expected, schemaNo int64) (DraftState, error) {
	return e.runCandidate(ctx, req, draftCandidateTarget{typeKey, itemID, expected, schemaNo}, cms.OpContentReady, scopeShared, markSharedReady)
}

// MarkReadyForType records readiness for one locale and its shared dependencies.
func (e *DraftEditor) MarkReadyForType(ctx context.Context, req cms.AuthRequest, typeKey, itemID, locale string, expected, schemaNo int64) (DraftState, error) {
	return e.runCandidate(ctx, req, draftCandidateTarget{typeKey, itemID, expected, schemaNo}, cms.OpContentReady, "", func(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema, readable relationAccess) error {
		return markLocaleReady(ctx, tx, state, schema, locale, readable)
	})
}

// PublishForType publishes a snapshot of the authorized draft.
func (e *DraftEditor) PublishForType(ctx context.Context, req cms.AuthRequest, typeKey, itemID string, expected, schemaNo int64, intended []string) (DraftState, error) {
	return e.runCandidate(ctx, req, draftCandidateTarget{typeKey, itemID, expected, schemaNo}, cms.OpContentPublish, "", func(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema, readable relationAccess) error {
		return publishCandidate(ctx, tx, state, schema, intended, readable)
	})
}

func (e *DraftEditor) runCandidate(ctx context.Context, req cms.AuthRequest, target draftCandidateTarget, operation cms.Operation, fieldScope string, command draftCandidateCommand) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, cms.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out DraftState
	err := e.auth.WithDraftCandidate(ctx, req, operation, target.itemID, func(ctx context.Context, tx *sql.Tx, session cms.Session, itemID string) error {
		access, err := session.LoadContentItemAccess(ctx, tx, operation, itemID)
		if err != nil {
			return err
		}
		if access.TypeKey != target.typeKey {
			return cms.ErrNotFound
		}
		state, schema, err := loadAuthorizedCandidate(ctx, tx, target, access, itemID, fieldScope)
		if err != nil {
			return auditedCandidateError(err)
		}
		readable := func(relation Relation) (bool, error) {
			return cmsRelationVisible(ctx, tx, session, relation)
		}
		if err := command(ctx, tx, &state, schema, readable); err != nil {
			return auditedCandidateError(databaseError(err))
		}
		out, err = candidateResponse(ctx, tx, session, state, schema, operation)
		return err
	})
	return draftCommandResult(out, err)
}

func candidateResponse(ctx context.Context, tx *sql.Tx, session cms.Session, state DraftState, schema TypeSchema, operation cms.Operation) (DraftState, error) {
	read, err := session.LoadContentItemAccess(ctx, tx, cms.OpContentRead, state.ItemID)
	if err != nil && !errors.Is(err, cms.ErrNotFound) {
		return DraftState{}, err
	}
	out, err := draftUpdateResponse(ctx, tx, session, state, schema, read)
	if err == nil && operation == cms.OpContentPublish {
		out.PublicationID = state.PublicationID
	}
	return out, err
}

func loadAuthorizedCandidate(ctx context.Context, tx *sql.Tx, target draftCandidateTarget, access cms.ContentItemAccess, itemID, fieldScope string) (DraftState, TypeSchema, error) {
	if err := checkDraftReadSize(ctx, tx, itemID); err != nil {
		return DraftState{}, TypeSchema{}, err
	}
	state, err := loadDraft(ctx, tx, itemID)
	if err != nil {
		return DraftState{}, TypeSchema{}, databaseError(err)
	}
	schema, err := loadSchema(ctx, tx, state.TypeID, state.SchemaNo)
	if err != nil {
		return DraftState{}, TypeSchema{}, databaseError(err)
	}
	for _, field := range schema.Fields {
		if (fieldScope == "" || fieldScope == field.Scope) && !access.AllowsField(field.ID) {
			return DraftState{}, TypeSchema{}, cms.ErrPermissionDenied
		}
	}
	if err := validateDraftVersions(state, target.version, target.schema); err != nil {
		return DraftState{}, TypeSchema{}, err
	}
	current, err := currentSchema(ctx, tx, state)
	return state, current, databaseError(err)
}

// Preserve the content error while letting the CMS audit expected denials.
func auditedCandidateError(err error) error {
	var domain *Error
	if errors.As(err, &domain) {
		switch domain.Kind {
		case Validation:
			return errors.Join(cms.ErrValidation, err)
		case Conflict:
			return errors.Join(cms.ErrVersionConflict, err)
		case NotFound:
			return errors.Join(cms.ErrNotFound, err)
		}
	}
	return err
}
