package content

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"stepolonia/apps/api/internal/identity"
)

type CollectionIconChange struct {
	PreviousVersion int64
	Version         int64
	IconSVG         string
}

func CollectionIconVersion(icon string) string {
	return collectionVersionToken(icon)
}

func collectionVersionToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// UpdateCollectionIcon applies navigation metadata without writing a schema.
func (e *DraftEditor) UpdateCollectionIcon(ctx context.Context, req identity.AuthRequest, key, expected, source string) (CollectionIconChange, error) {
	if e == nil || e.auth == nil {
		return CollectionIconChange{}, identity.ErrSecurityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, draftCandidateTimeout)
	defer cancel()
	var out CollectionIconChange
	_, err := e.auth.WithCollectionRevision(ctx, req, false, func(ctx context.Context, tx *sql.Tx) (identity.CollectionMutation, error) {
		var id, current string
		err := tx.QueryRowContext(ctx, `SELECT id::text,icon_svg,version FROM public.content_types WHERE key=$1`, key).
			Scan(&id, &current, &out.PreviousVersion)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(databaseError(err))
		}
		if CollectionIconVersion(current) != expected {
			return identity.CollectionMutation{}, auditedCandidateError(problem(Conflict, versionConflictCode))
		}
		out.IconSVG, err = normalizeCollectionIcon(source)
		if err != nil {
			return identity.CollectionMutation{}, auditedCandidateError(err)
		}
		out.Version = out.PreviousVersion
		if current != out.IconSVG {
			err = tx.QueryRowContext(ctx, `UPDATE public.content_types SET icon_svg=$2,version=version+1 WHERE id=$1 RETURNING version`, id, out.IconSVG).Scan(&out.Version)
			if err != nil {
				return identity.CollectionMutation{}, databaseError(err)
			}
		}
		return identity.CollectionMutation{TypeID: id}, nil
	})
	if err != nil {
		_, err = draftCommandResult(DraftState{}, err)
		return CollectionIconChange{}, err
	}
	return out, nil
}
