package identity

import (
	"context"
	"database/sql"
	"time"
)

type authBudget struct {
	at       time.Time
	keys     []attemptBudgetKey
	hashJobs int
}

type attemptBudgetKey struct {
	kind  string
	value string
	max   int
}

// Limits reserve persistent capacity before expensive authentication work.
// Success releases the exact reservation. Interrupted work retains its slot.
func (s *AuthService) limit(ctx context.Context, req AuthRequest, account string) (authBudget, error) {
	return s.limitScope(ctx, req, RateCredentials, account)
}

func (s *AuthService) limitScope(ctx context.Context, req AuthRequest, scope, subject string) (authBudget, error) {
	var budget authBudget
	if !req.ClientIP.IsValid() {
		return budget, ErrInvalidCredentials
	}
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		budget, err = reserveAuthBudget(ctx, tx, scope, subject, req.ClientIP.Unmap().String(), s.config.Site)
		return err
	})
	return budget, err
}

func reserveAuthBudget(ctx context.Context, tx *sql.Tx, scope, subject, ip, site string) (authBudget, error) {
	policy, err := readRatePolicy(ctx, tx, scope)
	if err != nil {
		return authBudget{}, err
	}
	budget := authBudget{keys: policy.budgetKeys(subject, ip, site)}
	if policy.HashJobs != nil {
		budget.hashJobs = *policy.HashJobs
	} else if scope == RateStepUp {
		// A current full session can use spare process capacity for policy recovery.
		budget.hashJobs = MaxPasswordHashJobs
	}
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&budget.at); err != nil {
		return authBudget{}, err
	}
	if err := budget.check(ctx, tx, policy.WindowSeconds); err != nil {
		return authBudget{}, err
	}
	if err := cleanupAttemptBuckets(ctx, tx, budget.at); err != nil {
		return authBudget{}, err
	}
	for _, key := range budget.keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.auth_attempt_buckets(bucket_start,kind,key_digest,count) VALUES($1,$2,$3,1)`,
			budget.at, key.kind, digest(key.value)); err != nil {
			return authBudget{}, err
		}
	}
	return budget, nil
}

func cleanupAttemptBuckets(ctx context.Context, tx *sql.Tx, at time.Time) error {
	// Retain enough history for a policy change to the longest allowed window.
	_, err := tx.ExecContext(ctx, `DELETE FROM public.auth_attempt_buckets WHERE (bucket_start,kind,key_digest) IN
 (SELECT bucket_start,kind,key_digest FROM public.auth_attempt_buckets
 WHERE bucket_start<=$1::timestamptz-$2::integer*interval '1 second' ORDER BY bucket_start LIMIT 1000)`, at, MaxRateWindowSeconds)
	return err
}

func (p RatePolicy) budgetKeys(subject, ip, site string) []attemptBudgetKey {
	switch p.Scope {
	case RatePreauth:
		return []attemptBudgetKey{{"preauth_ip", ip, p.IPAttempts}, {"preauth_site", site, p.InstallationAttempts}}
	case RateDenialAudit:
		return []attemptBudgetKey{{"denial_action", subject, *p.SubjectAttempts}, {"denial_ip", ip, p.IPAttempts}, {"denial_site", site, p.InstallationAttempts}}
	case RateStepUp:
		return []attemptBudgetKey{{"step_up_account", subject, *p.SubjectAttempts}, {"step_up_ip", ip, p.IPAttempts}, {"step_up_site", site, p.InstallationAttempts}}
	default:
		return []attemptBudgetKey{{"account", subject, *p.SubjectAttempts}, {"ip", ip, p.IPAttempts}, {"site", site, p.InstallationAttempts}}
	}
}

func (b authBudget) check(ctx context.Context, tx *sql.Tx, windowSeconds int) error {
	retryAfter := 0
	for _, key := range b.keys {
		var delay int
		// The newest entry crossing the budget is the last entry that must expire.
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(ceil(extract(epoch FROM max(bucket_start)-$3::timestamptz))::integer+$4,0)
 FROM (
  SELECT bucket_start,sum(count) OVER (ORDER BY bucket_start DESC) AS recent_attempts
  FROM public.auth_attempt_buckets
  WHERE kind=$1 AND key_digest=$2 AND bucket_start>$3::timestamptz-$4::integer*interval '1 second'
 ) AS recent WHERE recent_attempts >= $5`, key.kind, digest(key.value), b.at, windowSeconds, key.max).Scan(&delay)
		if err != nil {
			return err
		}
		retryAfter = max(retryAfter, delay)
	}
	if retryAfter > 0 {
		return rateLimitError{retryAfter: retryAfter}
	}
	return nil
}

func (b authBudget) success(ctx context.Context, tx *sql.Tx) error {
	for _, key := range b.keys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM public.auth_attempt_buckets WHERE bucket_start=$1 AND kind=$2 AND key_digest=$3`,
			b.at, key.kind, digest(key.value)); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthService) releaseBudget(ctx context.Context, budget authBudget) error {
	return s.transaction(ctx, budget.success)
}
