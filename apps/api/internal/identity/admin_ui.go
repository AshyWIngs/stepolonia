package identity

import (
	"context"
	"database/sql"
)

// ReadAdminUI verifies a full session and exposes published slug suggestions to
// authors, schema managers and settings managers with active grants.
func (s *AuthService) ReadAdminUI(ctx context.Context, req AuthRequest, read func(context.Context, Session, bool) error) error {
	if read == nil {
		return ErrSecurityUnavailable
	}
	return s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		var slugSuggestions bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM public.user_roles m JOIN public.roles r ON r.id=m.role_id AND r.state='active'
 JOIN public.role_grants g ON g.role_id=r.id
 JOIN public.permission_operations o ON o.key=g.operation AND o.scope=g.scope
 WHERE m.user_id=$1 AND (
 (g.scope='installation' AND g.operation IN ($2,$3)) OR
 (g.scope='content' AND g.operation IN ($4,$5) AND
 EXISTS (SELECT 1 FROM public.content_types t WHERE t.id=g.type_id AND t.enabled))))`,
			actor.user, opSettingsManage, OpContentTypesManage, opContentCreate, OpContentUpdate).Scan(&slugSuggestions)
		if err != nil {
			return err
		}
		return read(ctx, actor, slugSuggestions)
	})
}
