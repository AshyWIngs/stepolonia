package content

import (
	"context"
	"database/sql"

	"stepolonia/apps/api/internal/identity"
)

// AdoptSchemaForType pins the draft to the requested current schema when the
// actor can update every field in that schema.
func (e *DraftEditor) AdoptSchemaForType(ctx context.Context, req identity.AuthRequest, typeKey, itemID string, expected, schemaNo int64) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out DraftState
	err := e.auth.WithDraftSchemaAdoption(ctx, req, itemID, func(ctx context.Context, tx *sql.Tx, session identity.Session, itemID string) error {
		state, access, err := loadDraftForTransition(ctx, tx, session, identity.OpContentUpdate, typeKey, itemID)
		if err != nil {
			return auditedCandidateError(err)
		}
		schema, err := loadSchema(ctx, tx, state.TypeID, 0)
		if err != nil {
			return auditedCandidateError(databaseError(err))
		}
		if err := adoptAuthorizedSchema(ctx, tx, &state, schema, access, expected, schemaNo); err != nil {
			return auditedCandidateError(err)
		}
		out, err = candidateResponse(ctx, tx, session, state, schema, identity.OpContentUpdate)
		return err
	})
	return draftCommandResult(out, err)
}

func adoptAuthorizedSchema(ctx context.Context, tx *sql.Tx, state *DraftState, schema TypeSchema, access identity.ContentItemAccess, expected, targetSchema int64) error {
	for _, field := range schema.Fields {
		if !access.AllowsField(field.ID) {
			return identity.ErrPermissionDenied
		}
	}
	if expected != state.Version {
		return problem(Conflict, versionConflictCode)
	}
	if targetSchema != schema.SchemaNo {
		return problem(Conflict, schemaConflictCode)
	}
	if state.SchemaNo == targetSchema {
		return nil
	}
	return databaseError(adoptDraftSchema(ctx, tx, state, targetSchema))
}

// UnpublishForType clears the public pointer and resets readiness. It can
// remove a publication even when the draft's pinned schema is no longer current.
func (e *DraftEditor) UnpublishForType(ctx context.Context, req identity.AuthRequest, typeKey, itemID string, expected, schemaNo int64) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out DraftState
	err := e.auth.WithDraftCandidate(ctx, req, identity.OpContentUnpublish, itemID, func(ctx context.Context, tx *sql.Tx, session identity.Session, itemID string) error {
		state, _, err := loadDraftForTransition(ctx, tx, session, identity.OpContentUnpublish, typeKey, itemID)
		if err != nil {
			return auditedCandidateError(err)
		}
		if err := validateDraftVersions(state, expected, schemaNo); err != nil {
			return auditedCandidateError(err)
		}
		schema, err := loadSchema(ctx, tx, state.TypeID, schemaNo)
		if err != nil {
			return auditedCandidateError(databaseError(err))
		}
		if err := unpublishDraft(ctx, tx, &state); err != nil {
			return auditedCandidateError(databaseError(err))
		}
		out, err = candidateResponse(ctx, tx, session, state, schema, identity.OpContentUnpublish)
		return err
	})
	return draftCommandResult(out, err)
}

func loadDraftForTransition(ctx context.Context, tx *sql.Tx, session identity.Session, op identity.Operation, typeKey, itemID string) (DraftState, identity.ContentItemAccess, error) {
	access, err := session.LoadContentItemAccess(ctx, tx, op, itemID)
	if err != nil {
		return DraftState{}, access, err
	}
	if typeKey != access.TypeKey {
		return DraftState{}, access, identity.ErrNotFound
	}
	if err := checkDraftReadSize(ctx, tx, itemID); err != nil {
		return DraftState{}, access, err
	}
	state, err := loadDraft(ctx, tx, itemID)
	return state, access, databaseError(err)
}
