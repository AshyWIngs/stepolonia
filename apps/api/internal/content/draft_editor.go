package content

import (
	"context"
	"database/sql"
	"errors"

	"stepolonia/apps/api/internal/cms"
)

// DraftEditor is the CMS service for editing existing drafts.
type DraftEditor struct {
	auth *cms.AuthService
}

// NewDraftEditor binds the editor to an AuthService on the verified CMS Engine pool.
func NewDraftEditor(auth *cms.AuthService) *DraftEditor {
	return &DraftEditor{auth: auth}
}

// SaveDraft sets or unsets scalar fields using current CMS item and field permissions.
func (e *DraftEditor) SaveDraft(ctx context.Context, req cms.AuthRequest, itemID string, expected int64, patch DraftPatch) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, cms.ErrSecurityUnavailable
	}
	var out DraftState
	err := e.auth.WithDraftUpdate(ctx, req, itemID, func(ctx context.Context, tx *sql.Tx, session cms.Session, itemID string) error {
		update, err := session.LoadContentItemAccess(ctx, tx, cms.OpContentUpdate, itemID)
		if err != nil {
			return err
		}
		out, err = saveAuthorizedDraft(ctx, tx, session, update, itemID, expected, patch)
		return err
	})
	if err != nil {
		var domain *Error
		if errors.As(err, &domain) && domain.Kind != Unavailable {
			return DraftState{}, domain
		}
		if conflict := transactionConflict(err); conflict != nil {
			return DraftState{}, conflict
		}
		return DraftState{}, err
	}
	return out, nil
}

func saveAuthorizedDraft(ctx context.Context, tx *sql.Tx, session cms.Session, update cms.ContentItemAccess, itemID string, expected int64, patch DraftPatch) (DraftState, error) {
	state, err := loadDraft(ctx, tx, itemID)
	if err != nil {
		return DraftState{}, databaseError(err)
	}
	schema, err := loadSchema(ctx, tx, state.TypeID, state.SchemaNo)
	if err != nil {
		return DraftState{}, databaseError(err)
	}
	if err := checkDraftPatchFields(update, schema, patch); err != nil {
		return DraftState{}, err
	}
	if err := validateDraftVersions(state, expected, patch.SchemaNo); err != nil {
		return DraftState{}, err
	}
	read, err := session.LoadContentItemAccess(ctx, tx, cms.OpContentRead, itemID)
	if err != nil && !errors.Is(err, cms.ErrNotFound) {
		return DraftState{}, err
	}
	writeOnly := make(map[string]bool)
	for _, field := range schema.Fields {
		if update.AllowsField(field.ID) && !read.AllowsField(field.ID) {
			writeOnly[field.Key] = true
		}
	}
	if err := applyDraftPatch(ctx, tx, &state, schema, patch, writeOnly); err != nil {
		return DraftState{}, databaseError(err)
	}
	return draftUpdateResponse(ctx, tx, session, state, schema, read)
}

func draftUpdateResponse(ctx context.Context, tx *sql.Tx, session cms.Session, state DraftState, schema TypeSchema, read cms.ContentItemAccess) (DraftState, error) {
	if read.TypeID == "" {
		// A writer without read access gets only an optimistic version receipt.
		return DraftState{ItemID: state.ItemID, TypeID: state.TypeID, SchemaNo: state.SchemaNo, Version: state.Version}, nil
	}
	state, err := loadDraft(ctx, tx, state.ItemID)
	if err != nil {
		return DraftState{}, databaseError(err)
	}
	return projectDraftState(state, schema, read.AllowsField, func(relation Relation) (bool, error) {
		return cmsRelationVisible(ctx, tx, session, relation)
	})
}

func checkDraftPatchFields(update cms.ContentItemAccess, schema TypeSchema, patch DraftPatch) error {
	if patch.ReplaceRelations || len(patch.Relations) > 0 {
		return problem(Validation, "relation_edits_unavailable")
	}
	fieldsByKey := make(map[string]string, len(schema.Fields))
	for _, field := range schema.Fields {
		fieldsByKey[field.Key] = field.ID
	}
	for _, key := range patchKeys(schema, patch) {
		if !update.AllowsField(fieldsByKey[key]) {
			return cms.ErrPermissionDenied
		}
	}
	return nil
}

func cmsRelationVisible(ctx context.Context, tx *sql.Tx, session cms.Session, relation Relation) (bool, error) {
	access, err := session.LoadContentItemAccess(ctx, tx, cms.OpContentRead, relation.TargetItemID)
	if errors.Is(err, cms.ErrNotFound) {
		return false, nil
	}
	return access.TypeID == relation.TargetTypeID, err
}
