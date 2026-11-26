package identity

import (
	"context"
	"database/sql"
	"errors"
)

const (
	DefaultSessionIdleMinutes = 30
	DefaultSessionHours       = 12
	MinSessionIdleMinutes     = 15
	MaxSessionIdleMinutes     = 60
	MinSessionHours           = 1
	MaxSessionHours           = 12
)

const sessionPolicyChanged = "security.session_policy_changed"

type SessionPolicy struct {
	Version              int64 `json:"version,string"`
	IdleTimeoutMinutes   int   `json:"idle_timeout_minutes" doc:"Idle timeout in minutes for newly issued full sessions."`
	AbsoluteTimeoutHours int   `json:"absolute_timeout_hours" doc:"Absolute lifetime in hours for newly issued full sessions."`
}

func readSessionPolicy(ctx context.Context, tx *sql.Tx) (SessionPolicy, error) {
	var policy SessionPolicy
	err := tx.QueryRowContext(ctx, `SELECT version,idle_timeout_minutes,absolute_timeout_hours
 FROM public.session_policy WHERE id=1`).Scan(&policy.Version, &policy.IdleTimeoutMinutes, &policy.AbsoluteTimeoutHours)
	if err != nil {
		return SessionPolicy{}, err
	}
	if !validSessionPolicy(policy) {
		return SessionPolicy{}, ErrSecurityUnavailable
	}
	return policy, nil
}

// ReadSessionPolicy requires the installation policy grant.
func (s *AuthService) ReadSessionPolicy(ctx context.Context, req AuthRequest) (SessionPolicy, error) {
	var policy SessionPolicy
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		if err := requirePermission(ctx, tx, actor.user, opSessionPolicyManage); err != nil {
			return err
		}
		var err error
		policy, err = readSessionPolicy(ctx, tx)
		return err
	})
	if err != nil {
		return SessionPolicy{}, err
	}
	return policy, nil
}

// ReplaceSessionPolicy changes only the limits for future full logins.
func (s *AuthService) ReplaceSessionPolicy(ctx context.Context, req AuthRequest, desired SessionPolicy) (SessionPolicy, error) {
	var policy SessionPolicy
	err := s.authorizedCommand(ctx, req, []string{opSessionPolicyManage}, sessionPolicyChanged, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		if !validSessionPolicy(desired) {
			return ErrValidation
		}
		err := tx.QueryRowContext(ctx, `UPDATE public.session_policy
 SET idle_timeout_minutes=$1,absolute_timeout_hours=$2,version=version+1
 WHERE id=1 AND version=$3 RETURNING version,idle_timeout_minutes,absolute_timeout_hours`,
			desired.IdleTimeoutMinutes, desired.AbsoluteTimeoutHours, desired.Version).Scan(
			&policy.Version, &policy.IdleTimeoutMinutes, &policy.AbsoluteTimeoutHours)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		return securityAudit(ctx, tx, sessionPolicyChanged, actor.user, "", "")
	})
	if err != nil {
		return SessionPolicy{}, err
	}
	return policy, nil
}

func validSessionPolicy(policy SessionPolicy) bool {
	return policy.Version > 0 &&
		policy.IdleTimeoutMinutes >= MinSessionIdleMinutes && policy.IdleTimeoutMinutes <= MaxSessionIdleMinutes &&
		policy.AbsoluteTimeoutHours >= MinSessionHours && policy.AbsoluteTimeoutHours <= MaxSessionHours &&
		policy.IdleTimeoutMinutes <= policy.AbsoluteTimeoutHours*60
}
