package content

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"stepolonia/apps/api/internal/identity"
)

// DraftEditor creates, reads and edits content drafts.
type DraftEditor struct {
	auth *identity.AuthService
}

// NewDraftEditor binds the editor to an AuthService on the verified content editor pool.
func NewDraftEditor(auth *identity.AuthService) *DraftEditor {
	return &DraftEditor{auth: auth}
}

// CreateDraftForType creates an empty draft at the expected current schema.
// The response is a receipt. Reading the draft requires a separate read grant.
func (e *DraftEditor) CreateDraftForType(ctx context.Context, req identity.AuthRequest, typeKey string, schemaNo int64) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, identity.ErrSecurityUnavailable
	}
	var receipt DraftState
	err := e.auth.WithDraftCreate(ctx, req, typeKey, schemaNo, func(ctx context.Context, tx *sql.Tx, typeID, itemID string) error {
		version, err := insertEmptyDraft(ctx, tx, typeID, itemID, schemaNo)
		if err != nil {
			return err
		}
		receipt = DraftState{ItemID: itemID, TypeID: typeID, SchemaNo: schemaNo, Version: version}
		return nil
	})
	if errors.Is(err, identity.ErrDraftSchemaConflict) {
		return DraftState{}, problem(Conflict, schemaConflictCode)
	}
	if err != nil {
		return DraftState{}, err
	}
	return receipt, nil
}

const (
	// MaxDraftLocales is the supported number of locales in a draft.
	MaxDraftLocales  = 16
	draftReadTimeout = 8 * time.Second
)

// ListDraftsForType returns one page of draft identities visible to the actor.
func (e *DraftEditor) ListDraftsForType(ctx context.Context, req identity.AuthRequest, typeKey string, limit, offset int) (identity.DraftPage, error) {
	if e == nil || e.auth == nil {
		return identity.DraftPage{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	return e.auth.ReadDraftPage(ctx, req, typeKey, limit, offset)
}

// ReadDraftForType returns the actor-readable draft for the persisted collection.
func (e *DraftEditor) ReadDraftForType(ctx context.Context, req identity.AuthRequest, typeKey, itemID string) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftReadTimeout)
	defer cancel()
	var out DraftState
	err := e.auth.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session identity.Session) error {
		read, err := session.LoadContentItemAccess(ctx, tx, identity.OpContentRead, itemID)
		if err != nil {
			return err
		}
		if read.TypeKey != typeKey {
			return identity.ErrNotFound
		}
		if err := checkDraftReadSize(ctx, tx, itemID); err != nil {
			return err
		}
		state, err := loadDraft(ctx, tx, itemID)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrNotFound
		}
		if err != nil {
			return err
		}
		schema, err := loadSchema(ctx, tx, state.TypeID, state.SchemaNo)
		if err != nil {
			return err
		}
		out, err = projectAuthorizedDraft(ctx, tx, session, state, schema, read)
		return err
	})
	if err != nil {
		return DraftState{}, err
	}
	return out, nil
}

// Bound locale rows before loading their values. The schema caps each locale's
// JSON size, and each relation belongs to a schema field and at most one locale.
func checkDraftReadSize(ctx context.Context, tx *sql.Tx, itemID string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (
 SELECT 1 FROM public.content_draft_locales WHERE item_id=$1 LIMIT $2
 ) AS draft_locales`, itemID, MaxDraftLocales+1).Scan(&count); err != nil {
		return err
	}
	if count > MaxDraftLocales {
		return identity.ErrSecurityUnavailable
	}
	return nil
}

// SaveDraft sets or unsets scalar fields using current content item and field permissions.
func (e *DraftEditor) SaveDraft(ctx context.Context, req identity.AuthRequest, itemID string, expected int64, patch DraftPatch) (DraftState, error) {
	return e.saveDraft(ctx, req, "", itemID, expected, patch)
}

// SaveDraftForType binds the requested collection key to the authorized item.
func (e *DraftEditor) SaveDraftForType(ctx context.Context, req identity.AuthRequest, typeKey, itemID string, expected int64, patch DraftPatch) (DraftState, error) {
	if typeKey == "" {
		return DraftState{}, identity.ErrNotFound
	}
	return e.saveDraft(ctx, req, typeKey, itemID, expected, patch)
}

func (e *DraftEditor) saveDraft(ctx context.Context, req identity.AuthRequest, typeKey, itemID string, expected int64, patch DraftPatch) (DraftState, error) {
	if e == nil || e.auth == nil {
		return DraftState{}, identity.ErrSecurityUnavailable
	}
	var out DraftState
	err := e.auth.WithDraftUpdate(ctx, req, itemID, func(ctx context.Context, tx *sql.Tx, session identity.Session, itemID string) error {
		update, err := session.LoadContentItemAccess(ctx, tx, identity.OpContentUpdate, itemID)
		if err != nil {
			return err
		}
		if typeKey != "" && update.TypeKey != typeKey {
			return identity.ErrNotFound
		}
		out, err = saveAuthorizedDraft(ctx, tx, session, update, itemID, expected, patch)
		return err
	})
	return draftCommandResult(out, err)
}

func draftCommandResult(out DraftState, err error) (DraftState, error) {
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

func saveAuthorizedDraft(ctx context.Context, tx *sql.Tx, session identity.Session, update identity.ContentItemAccess, itemID string, expected int64, patch DraftPatch) (DraftState, error) {
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
	read, err := session.LoadContentItemAccess(ctx, tx, identity.OpContentRead, itemID)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		return DraftState{}, err
	}
	writeOnly := make(map[string]bool)
	for _, field := range schema.Fields {
		if update.AllowsField(field.ID) && !read.AllowsField(field.ID) {
			writeOnly[field.Key] = true
		}
	}
	if err := applyDraftPatch(ctx, tx, &state, schema, patch, writeOnly, func(relation Relation) (bool, error) {
		return canReadRelation(ctx, tx, session, relation)
	}); err != nil {
		return DraftState{}, databaseError(err)
	}
	return draftUpdateResponse(ctx, tx, session, state, schema, read)
}

func draftUpdateResponse(ctx context.Context, tx *sql.Tx, session identity.Session, state DraftState, schema TypeSchema, read identity.ContentItemAccess) (DraftState, error) {
	if read.TypeID == "" {
		// A writer without read access gets only an optimistic version receipt.
		return DraftState{ItemID: state.ItemID, TypeID: state.TypeID, SchemaNo: state.SchemaNo, Version: state.Version}, nil
	}
	state, err := loadDraft(ctx, tx, state.ItemID)
	if err != nil {
		return DraftState{}, databaseError(err)
	}
	return projectAuthorizedDraft(ctx, tx, session, state, schema, read)
}

func projectAuthorizedDraft(ctx context.Context, tx *sql.Tx, session identity.Session, state DraftState, schema TypeSchema, read identity.ContentItemAccess) (DraftState, error) {
	return projectDraftState(state, schema, read.AllowsField, func(relation Relation) (bool, error) {
		return canReadRelation(ctx, tx, session, relation)
	})
}

func checkDraftPatchFields(update identity.ContentItemAccess, schema TypeSchema, patch DraftPatch) error {
	if patch.ReplaceRelations || len(patch.Relations) > 0 {
		return problem(Validation, "relation_edits_unavailable")
	}
	fieldsByKey := make(map[string]string, len(schema.Fields))
	for _, field := range schema.Fields {
		fieldsByKey[field.Key] = field.ID
	}
	for _, key := range patchKeys(schema, patch) {
		if !update.AllowsField(fieldsByKey[key]) {
			return identity.ErrPermissionDenied
		}
	}
	for _, change := range patch.RelationChanges {
		if !update.AllowsField(change.FieldID) {
			return identity.ErrPermissionDenied
		}
	}
	return nil
}

func canReadRelation(ctx context.Context, tx *sql.Tx, session identity.Session, relation Relation) (bool, error) {
	access, err := session.LoadContentItemAccess(ctx, tx, identity.OpContentRead, relation.TargetItemID)
	if errors.Is(err, identity.ErrNotFound) {
		return false, nil
	}
	return access.TypeID == relation.TargetTypeID, err
}
