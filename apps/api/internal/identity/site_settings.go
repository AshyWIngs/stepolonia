package identity

import (
	"context"
	"database/sql"
	"errors"
)

const sitePublishedEvent = "settings.published"

// ReadSiteSettings verifies the current settings grant before loading a draft.
func (s *AuthService) ReadSiteSettings(ctx context.Context, req AuthRequest, read func(context.Context, *sql.Tx) error) error {
	return s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requirePermission(ctx, tx, session.user, opSettingsManage); err != nil {
			return err
		}
		return read(ctx, tx)
	})
}

func (s *AuthService) ApplySiteSettings(ctx context.Context, req AuthRequest, write func(context.Context, *sql.Tx) error) error {
	if write == nil {
		return ErrSecurityUnavailable
	}
	var authorizedFailure error
	err := s.auditedCommand(ctx, req, sitePublishedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requirePermission(ctx, tx, session.user, opSettingsManage); err != nil {
			return err
		}
		if err := write(ctx, tx); err != nil {
			if errors.Is(err, ErrValidation) || errors.Is(err, ErrVersionConflict) {
				authorizedFailure = err
			}
			return err
		}
		return securityAudit(ctx, tx, sitePublishedEvent, session.user, "", "")
	})
	if authorizedFailure != nil && errors.Is(err, ErrRateLimited) {
		return authorizedFailure
	}
	return err
}
