package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	opSessionsRevokeAny  = "sessions.revoke_any"
	opAuditRead          = "audit.read"
	sessionRevokedEvent  = "auth.session_revoked"
	sessionsRevokedEvent = "auth.sessions_revoked"
)

const (
	DefaultAccountPageLimit = 20
	MaxAccountPageLimit     = 100
	MaxAccountPageOffset    = 10_000
)

type AccountPage struct{ Limit, Offset int }

func (p AccountPage) normalize() (AccountPage, error) {
	if p.Limit == 0 {
		p.Limit = DefaultAccountPageLimit
	}
	if p.Limit < 1 || p.Limit > MaxAccountPageLimit || p.Offset < 0 || p.Offset > MaxAccountPageOffset {
		return p, ErrValidation
	}
	return p, nil
}

type AccountProfile struct {
	ID, Login              string
	MFAEnabled             bool
	RecoveryCodesRemaining int
}

type AccountSession struct {
	ID                             string
	CreatedAt, LastSeen, ExpiresAt time.Time
	Current                        bool
}

type SecurityAuditEntry struct {
	ID, Operation, Result, Reason string
	ActorID, TargetID, RequestID  string
	OccurredAt                    time.Time
}

func (s *AuthService) ReadAccountProfile(ctx context.Context, req AuthRequest) (AccountProfile, error) {
	var out AccountProfile
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		return tx.QueryRowContext(ctx, `SELECT id::text,login_key,
 EXISTS(SELECT 1 FROM public.mfa_totp m WHERE m.user_id=u.id AND m.confirmed_at IS NOT NULL),
 (SELECT count(*) FROM public.recovery_codes c WHERE c.user_id=u.id AND c.consumed_at IS NULL)
 FROM public.users u WHERE id=$1`, session.user).Scan(&out.ID, &out.Login, &out.MFAEnabled, &out.RecoveryCodesRemaining)
	})
	return out, err
}

func (s *AuthService) ListAccountSessions(ctx context.Context, req AuthRequest, userID string, page AccountPage) ([]AccountSession, error) {
	out := make([]AccountSession, 0)
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		owner, err := accountSessionOwner(ctx, tx, session, userID)
		if err != nil {
			return err
		}
		page, err = page.normalize()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id::text,created_at,last_seen,expires_at,id=$1
 FROM public.sessions s WHERE user_id=$2 AND kind='full' AND revoked_at IS NULL
 AND credential_version=(SELECT credential_version FROM public.users WHERE id=$2)
 AND expires_at>$3 AND last_seen>$3-interval '30 minutes' AND failed_attempts<5
 ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, session.id, owner, session.now, page.Limit, page.Offset)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item AccountSession
			if err := rows.Scan(&item.ID, &item.CreatedAt, &item.LastSeen, &item.ExpiresAt, &item.Current); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func accountSessionOwner(ctx context.Context, tx *sql.Tx, session Session, userID string) (string, error) {
	if userID == "" || userID == session.user {
		return session.user, nil
	}
	account, err := findUser(ctx, tx, userID)
	if err != nil {
		return "", err
	}
	if account.ID == session.user {
		return session.user, nil
	}
	if account.State != userStateActive {
		return "", ErrNotFound
	}
	if err := authorizeForeignSession(ctx, tx, session, account.ID); err != nil {
		return "", err
	}
	return account.ID, nil
}

func (s *AuthService) RevokeAccountSession(ctx context.Context, req AuthRequest, id string) (bool, error) {
	var current bool
	err := s.auditedCommand(ctx, req, sessionRevokedEvent, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		var parsed pgtype.UUID
		if parsed.Scan(id) != nil || !parsed.Valid {
			return ErrValidation
		}
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT s.user_id::text FROM public.sessions s JOIN public.users u ON u.id=s.user_id
 WHERE s.id=$1 AND s.kind='full' AND s.revoked_at IS NULL AND s.credential_version=u.credential_version
 AND u.state='active' AND s.expires_at>$2 AND s.last_seen>$2-interval '30 minutes' AND s.failed_attempts<5`, id, actor.now).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if owner != actor.user {
			if err := authorizeForeignSession(ctx, tx, actor, owner); err != nil {
				return err
			}
		}
		if err := revokeSession(ctx, tx, id); err != nil {
			return err
		}
		current = parsed.String() == actor.id
		return securityAudit(ctx, tx, sessionRevokedEvent, actor.user, owner, "")
	})
	return current && err == nil, err
}

func authorizeForeignSession(ctx context.Context, tx *sql.Tx, actor Session, owner string) error {
	if err := requirePermission(ctx, tx, actor.user, opSessionsRevokeAny); err != nil {
		if errors.Is(err, ErrPermissionDenied) {
			return ErrNotFound
		}
		return err
	}
	snapshot, err := loadAccess(ctx, tx)
	if err != nil {
		return err
	}
	if !withinEnvelope(snapshot[actor.user].active, snapshot[owner].latent) {
		return ErrNotFound
	}
	if !actor.FreshStepUp() {
		return ErrStepUpRequired
	}
	return nil
}

func (s *AuthService) ListSecurityAudit(ctx context.Context, req AuthRequest, page AccountPage) ([]SecurityAuditEntry, error) {
	out := make([]SecurityAuditEntry, 0)
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		if err := requirePermission(ctx, tx, actor.user, opAuditRead); err != nil {
			return err
		}
		page, err := page.normalize()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id::text,operation,result,reason,occurred_at,
 COALESCE(actor_id::text,''),COALESCE(target_id::text,''),COALESCE(request_id::text,'')
 FROM public.security_audit ORDER BY occurred_at DESC,id DESC LIMIT $1 OFFSET $2`, page.Limit, page.Offset)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item SecurityAuditEntry
			if err := rows.Scan(&item.ID, &item.Operation, &item.Result, &item.Reason, &item.OccurredAt, &item.ActorID, &item.TargetID, &item.RequestID); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
