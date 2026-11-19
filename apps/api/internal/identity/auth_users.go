package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const opUsersCreate = "users.create"

const (
	userStateActive   = "active"
	userStateDisabled = "disabled"
	userStatePending  = "pending"
)

type auditRequestKey struct{}

func auditRequestContext(ctx context.Context, req AuthRequest) (context.Context, error) {
	id := req.RequestID
	if id == "" {
		var err error
		id, err = randomID()
		if err != nil {
			return ctx, authError(err)
		}
	} else {
		var parsed pgtype.UUID
		if parsed.Scan(id) != nil || !parsed.Valid {
			return ctx, ErrValidation
		}
	}
	return context.WithValue(ctx, auditRequestKey{}, id), nil
}

// User exposes provisioning state without credential material or memberships.
type User struct {
	ID, Login, State string
	Version          int64
}

// OnboardingToken is delivered once through the issuer's verified external channel.
type OnboardingToken struct {
	Token     string
	ExpiresAt time.Time
	User      User
}

func securityAudit(ctx context.Context, tx *sql.Tx, operation, actor, target, reason string) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	var actorID, targetID any
	if actor != "" {
		actorID = actor
	}
	if target != "" {
		targetID = target
	}
	result := "success"
	if reason != "" {
		result = "denied"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.security_audit(id,operation,actor_id,target_id,result,reason,request_id)VALUES($1,$2,$3,$4,$5,$6,$7)`, id, operation, actorID, targetID, result, reason, ctx.Value(auditRequestKey{}))
	return err
}

// securityCommand authorizes from current grants after the shared mutex. Denials
// are audited after rollback, so an audit outage never permits a partial command.
func (s *AuthService) securityCommand(ctx context.Context, req AuthRequest, operation string, fn func(context.Context, *sql.Tx, Session) error) error {
	return s.authorizedCommand(ctx, req, []string{opUsersCreate}, operation, fn)
}

func (s *AuthService) authorizedCommand(ctx context.Context, req AuthRequest, permissions []string, operation string, fn func(context.Context, *sql.Tx, Session) error) error {
	return s.authorizedCommandWithFreshness(ctx, req, permissions, operation, true, fn)
}

func (s *AuthService) authorizedCommandWithFreshness(ctx context.Context, req AuthRequest, permissions []string, operation string, requireFreshMFA bool, fn func(context.Context, *sql.Tx, Session) error) error {
	return s.auditedCommand(ctx, req, operation, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if len(permissions) == 0 {
			return ErrPermissionDenied
		}
		for _, permission := range permissions {
			if err := requirePermission(ctx, tx, x.user, permission); err != nil {
				return err
			}
		}
		if requireFreshMFA && !x.FreshStepUp() {
			return ErrStepUpRequired
		}
		return fn(ctx, tx, x)
	})
}

// The callback checks command-specific authorization and records success in the transaction.
func (s *AuthService) auditedCommand(ctx context.Context, req AuthRequest, operation string, fn func(context.Context, *sql.Tx, Session) error) error {
	ctx, err := auditRequestContext(ctx, req)
	if err != nil {
		return err
	}
	err = s.WithSession(ctx, req, true, fn)
	if err == nil || errors.Is(err, ErrSecurityUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return s.auditCommandDenial(ctx, req, operation, err)
}

func requirePermission(ctx context.Context, tx *sql.Tx, user, operation string) error {
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.user_roles ur JOIN public.roles r ON r.id=ur.role_id JOIN public.role_grants g ON g.role_id=r.id JOIN public.permission_operations o ON o.key=g.operation WHERE ur.user_id=$1 AND r.state='active' AND g.operation=$2 AND g.scope='installation' AND o.scope='installation')`, user, operation).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrPermissionDenied
	}
	return nil
}

func (s *AuthService) auditCommandDenial(ctx context.Context, req AuthRequest, operation string, denied error) error {
	// Invalid transport addresses cannot originate from the HTTP adapter. Keep
	// direct internal refusals fail-closed without unbounded anonymous audit.
	if !req.ClientIP.IsValid() {
		return denied
	}
	// The key excludes the bearer: rotating forged cookies cannot reset the
	// persistent per-command/address failure budget. Failed reservations remain.
	if _, err := s.limit(ctx, req, operation+":"+req.ClientIP.Unmap().String()); err != nil {
		return err
	}
	auditErr := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var actor string
		x, sessionErr := s.session(ctx, tx, req, sessionFull, false)
		if sessionErr == nil {
			actor = x.user
		} else if !errors.Is(sessionErr, ErrAuthenticationRequired) {
			return sessionErr
		}
		return securityAudit(ctx, tx, operation, actor, "", ErrInvalidCredentials.Error())
	})
	if auditErr != nil {
		return auditErr
	}
	return denied
}

// CreateUser creates a pending account without grants, credentials or a session.
func (s *AuthService) CreateUser(ctx context.Context, req AuthRequest, login string) (User, error) {
	var out User
	err := s.securityCommand(ctx, req, "users.created", func(ctx context.Context, tx *sql.Tx, x Session) error {
		key, err := normalizeLogin(login)
		if err != nil {
			return fmt.Errorf("%w: invalid login", ErrValidation)
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM public.users`).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return ErrInvalidTransition
		}
		out.ID, err = randomID()
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO public.users(id,login_key) VALUES($1,$2) RETURNING login_key,state,version`, out.ID, key).Scan(&out.Login, &out.State, &out.Version)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		return securityAudit(ctx, tx, "users.created", x.user, out.ID, "")
	})
	if err != nil {
		return User{}, err
	}
	return out, nil
}

// IssueOnboardingToken invalidates all earlier enrollment credentials atomically.
func (s *AuthService) IssueOnboardingToken(ctx context.Context, req AuthRequest, user string, expectedVersion int64) (OnboardingToken, error) {
	var out OnboardingToken
	err := s.securityCommand(ctx, req, "users.onboarding_issued", func(ctx context.Context, tx *sql.Tx, x Session) error {
		var err error
		out.User, err = onboardingTarget(ctx, tx, user, expectedVersion)
		if err != nil {
			return err
		}
		if err = revokeUser(ctx, tx, user); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE public.onboarding_tokens SET consumed_at=clock_timestamp() WHERE user_id=$1 AND consumed_at IS NULL`, user); err != nil {
			return err
		}
		var version int64
		if err = tx.QueryRowContext(ctx, `UPDATE public.users SET version=version+1,credential_version=credential_version+1 WHERE id=$1 RETURNING version,credential_version`, user).Scan(&out.User.Version, &version); err != nil {
			return err
		}
		out.Token, err = bearer()
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO public.onboarding_tokens(digest,user_id,credential_version,created_at,expires_at) SELECT $1,$2,$3,n,n+interval '24 hours' FROM (SELECT clock_timestamp() n)t RETURNING expires_at`, digest(out.Token), user, version).Scan(&out.ExpiresAt)
		if err != nil {
			return err
		}
		return securityAudit(ctx, tx, "users.onboarding_issued", x.user, user, "")
	})
	if err != nil {
		return OnboardingToken{}, err
	}
	return out, nil
}

func onboardingTarget(ctx context.Context, tx *sql.Tx, user string, expectedVersion int64) (User, error) {
	var out User
	var id pgtype.UUID
	if id.Scan(user) != nil || !id.Valid || expectedVersion < 1 {
		return out, ErrValidation
	}
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT id::text,login_key,state,version,state='pending' FROM public.users WHERE id=$1`, user).Scan(&out.ID, &out.Login, &out.State, &out.Version, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !pending {
		return out, ErrInvalidTransition
	}
	var memberships bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=$1)`, user).Scan(&memberships); err != nil {
		return out, err
	}
	// This slice never assigns pending users. Unknown latent access requires
	// the later delegation-envelope command path rather than a partial check.
	if memberships {
		return out, ErrNotFound
	}
	if out.Version != expectedVersion {
		return out, ErrVersionConflict
	}
	return out, nil
}
