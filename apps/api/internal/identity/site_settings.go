package identity

import (
	"context"
	"database/sql"
)

const (
	siteDraftSavedEvent = "settings.draft_saved"
	sitePublishedEvent  = "settings.published"
)

// ReadSiteSettings verifies the current settings grant before loading a draft.
func (s *AuthService) ReadSiteSettings(ctx context.Context, req AuthRequest, read func(context.Context, *sql.Tx) error) error {
	return s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requirePermission(ctx, tx, session.user, opSettingsManage); err != nil {
			return err
		}
		return read(ctx, tx)
	})
}

func (s *AuthService) SaveSiteSettings(ctx context.Context, req AuthRequest, save func(context.Context, *sql.Tx) error) error {
	return s.writeSiteSettings(ctx, req, siteDraftSavedEvent, save)
}

func (s *AuthService) PublishSiteSettings(ctx context.Context, req AuthRequest, publish func(context.Context, *sql.Tx) error) error {
	return s.writeSiteSettings(ctx, req, sitePublishedEvent, publish)
}

func (s *AuthService) writeSiteSettings(ctx context.Context, req AuthRequest, event string, write func(context.Context, *sql.Tx) error) error {
	if write == nil {
		return ErrSecurityUnavailable
	}
	return s.authorizedCommand(ctx, req, []string{opSettingsManage}, event, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := write(ctx, tx); err != nil {
			return err
		}
		return securityAudit(ctx, tx, event, session.user, "", "")
	})
}
