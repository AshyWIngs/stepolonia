package identity

import (
	"context"
	"database/sql"
	"errors"
)

const (
	RateCredentials = "credentials"
	RatePreauth     = "preauth"
	RateStepUp      = "step_up"
	RateDenialAudit = "denial_audit"

	DefaultRateWindowSeconds = 900
	DefaultSubjectAttempts   = 5
	DefaultIPAttempts        = 30
	DefaultSiteAttempts      = 100
	MinRateWindowSeconds     = 60
	MaxRateWindowSeconds     = 3600
	MaxSubjectAttempts       = 20
	MaxRateIPAttempts        = 120
	MaxRateSiteAttempts      = 400
	MaxPasswordHashJobs      = 4
)

const ratePolicyChanged = "security.rate_policy_changed"

type RatePolicy struct {
	Scope                string `json:"scope"`
	Version              int64  `json:"version,string"`
	WindowSeconds        int    `json:"window_seconds"`
	SubjectAttempts      *int   `json:"subject_attempts,omitempty"`
	IPAttempts           int    `json:"ip_attempts"`
	InstallationAttempts int    `json:"installation_attempts"`
	HashJobs             *int   `json:"hash_jobs,omitempty"`
}

type ratePolicyRow interface{ Scan(...any) error }

func scanRatePolicy(row ratePolicyRow) (RatePolicy, error) {
	var policy RatePolicy
	if err := row.Scan(&policy.Scope, &policy.Version, &policy.WindowSeconds, &policy.SubjectAttempts, &policy.IPAttempts, &policy.InstallationAttempts, &policy.HashJobs); err != nil {
		return policy, err
	}
	if !validRatePolicy(policy) {
		return RatePolicy{}, ErrSecurityUnavailable
	}
	return policy, nil
}

func readRatePolicy(ctx context.Context, tx *sql.Tx, scope string) (RatePolicy, error) {
	return scanRatePolicy(tx.QueryRowContext(ctx, `SELECT scope, version, window_seconds, subject_attempts,
 ip_attempts, installation_attempts, hash_jobs FROM public.auth_rate_policies WHERE scope=$1`, scope))
}

func validRatePolicy(policy RatePolicy) bool {
	if policy.Version < 1 || policy.WindowSeconds < MinRateWindowSeconds || policy.WindowSeconds > MaxRateWindowSeconds ||
		policy.IPAttempts < 1 || policy.IPAttempts > MaxRateIPAttempts ||
		policy.InstallationAttempts < 1 || policy.InstallationAttempts > MaxRateSiteAttempts {
		return false
	}
	if !validOptionalLimit(policy.HashJobs, policy.Scope == RateCredentials, MaxPasswordHashJobs) {
		return false
	}
	switch policy.Scope {
	case RateCredentials, RateStepUp, RateDenialAudit:
		return validOptionalLimit(policy.SubjectAttempts, true, MaxSubjectAttempts)
	case RatePreauth:
		return policy.SubjectAttempts == nil
	default:
		return false
	}
}

func validOptionalLimit(value *int, required bool, maximum int) bool {
	if required {
		return value != nil && *value >= 1 && *value <= maximum
	}
	return value == nil
}

func (s *AuthService) ReadRatePolicies(ctx context.Context, req AuthRequest) ([]RatePolicy, error) {
	var policies []RatePolicy
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		if err := requirePermission(ctx, tx, actor.user, opSessionPolicyManage); err != nil {
			return err
		}
		var err error
		policies, err = readRatePolicies(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return policies, nil
}

func readRatePolicies(ctx context.Context, tx *sql.Tx) ([]RatePolicy, error) {
	scopes := [...]string{RateCredentials, RatePreauth, RateStepUp, RateDenialAudit}
	policies := make([]RatePolicy, 0, len(scopes))
	for _, scope := range scopes {
		policy, err := readRatePolicy(ctx, tx, scope)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

func (s *AuthService) ReplaceRatePolicy(ctx context.Context, req AuthRequest, desired RatePolicy) (RatePolicy, error) {
	var updated RatePolicy
	err := s.authorizedCommand(ctx, req, []string{opSessionPolicyManage}, ratePolicyChanged, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		if !validRatePolicy(desired) {
			return ErrValidation
		}
		var err error
		updated, err = scanRatePolicy(tx.QueryRowContext(ctx, `UPDATE public.auth_rate_policies
 SET window_seconds=$1,subject_attempts=$2,ip_attempts=$3,installation_attempts=$4,hash_jobs=$5,version=version+1
 WHERE scope=$6 AND version=$7
 RETURNING scope, version, window_seconds, subject_attempts, ip_attempts, installation_attempts, hash_jobs`,
			desired.WindowSeconds, desired.SubjectAttempts,
			desired.IPAttempts, desired.InstallationAttempts, desired.HashJobs, desired.Scope, desired.Version))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		return securityAudit(ctx, tx, ratePolicyChanged, actor.user, "", "")
	})
	if err != nil {
		return RatePolicy{}, err
	}
	return updated, nil
}

type rateLimitError struct{ retryAfter int }

func (e rateLimitError) Error() string { return ErrRateLimited.Error() }
func (e rateLimitError) Unwrap() error { return ErrRateLimited }

func RateRetryAfter(err error) int {
	var limited rateLimitError
	if errors.As(err, &limited) {
		return limited.retryAfter
	}
	return DefaultRateWindowSeconds
}
