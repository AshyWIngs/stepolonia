package cms

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Enrollment exposes only the token owner's newly encrypted pending TOTP seed.
type Enrollment struct {
	SessionTokens
	Secret string
}

type onboardingSnapshot struct {
	user    string
	version int64
	expires time.Time
}

func readOnboarding(ctx context.Context, tx *sql.Tx, token string) (onboardingSnapshot, error) {
	var snap onboardingSnapshot
	if !validBearer(token) {
		return snap, ErrInvalidCredentials
	}
	err := tx.QueryRowContext(ctx, `SELECT u.id::text,u.credential_version,t.expires_at FROM public.onboarding_tokens t JOIN public.users u ON u.id=t.user_id WHERE t.digest=$1 AND t.consumed_at IS NULL AND t.expires_at>clock_timestamp() AND t.credential_version=u.credential_version AND u.state='pending' AND u.enrollment_completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.user_roles ur WHERE ur.user_id=u.id)`, digest(token)).Scan(&snap.user, &snap.version, &snap.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return snap, ErrInvalidCredentials
	}
	return snap, err
}

// StartEnrollment consumes a one-time token and creates a restricted session.
// Password hashing runs outside the mutex and is revalidated against its version.
func (s *AuthService) StartEnrollment(ctx context.Context, req AuthRequest, token, password string) (Enrollment, error) {
	var out Enrollment
	ctx, err := auditRequestContext(ctx, req)
	if err != nil {
		return out, err
	}
	if err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := s.session(ctx, tx, req, sessionPreauth, true)
		return err
	}); err != nil {
		return out, err
	}
	budget, err := s.limit(ctx, req, "onboarding:"+token)
	if err != nil {
		return out, err
	}
	var snap onboardingSnapshot
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		snap, err = readOnboarding(ctx, tx, token)
		return err
	})
	if errors.Is(err, ErrInvalidCredentials) {
		return out, s.denied(ctx, "")
	}
	if err != nil {
		return out, err
	}
	hash, err := enrollmentPassword(ctx, password)
	if err != nil {
		return out, err
	}
	secret, err := newSecret()
	if err != nil {
		return out, authError(err)
	}
	encrypted, err := encryptSeed(s.config.EncryptionKey, s.config.Site, snap.user, s.config.KeyVersion, secret)
	if err != nil {
		return out, authError(err)
	}
	command := enrollmentStart{snapshot: snap, budget: budget, token: token, hash: hash, encrypted: encrypted}
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.startEnrollmentTx(ctx, tx, req, command, &out.SessionTokens)
	})
	if errors.Is(err, ErrInvalidCredentials) {
		err = s.denied(ctx, snap.user)
	}
	if err != nil {
		return Enrollment{}, err
	}
	out.Secret = secret
	return out, nil
}

// CompleteEnrollment confirms the new factor and leaves the account disabled.
func (s *AuthService) CompleteEnrollment(ctx context.Context, req AuthRequest, code string) ([]string, error) {
	ctx, err := auditRequestContext(ctx, req)
	if err != nil {
		return nil, err
	}
	var account string
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, sessionEnrollment, true)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, authLoginSQL, x.user).Scan(&account)
	})
	if err != nil {
		return nil, err
	}
	budget, err := s.limit(ctx, req, account)
	if err != nil {
		return nil, err
	}
	codes, digests, err := recoveryCodes()
	if err != nil {
		return nil, authError(err)
	}
	var rejected bool
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.completeEnrollmentTx(ctx, tx, req, code, digests, budget, &rejected)
	})
	if err != nil {
		return nil, err
	}
	if rejected {
		return nil, ErrInvalidCredentials
	}
	return codes, nil
}

func endEnrollment(ctx context.Context, tx *sql.Tx, x Session) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.enrollment_attempts SET ended_at=clock_timestamp() WHERE session_id=$1 AND ended_at IS NULL`, x.id); err != nil {
		return err
	}
	return authAudit(ctx, tx, "auth.enrollment_cancelled", x.user, false)
}

func enrollmentPassword(ctx context.Context, password string) (string, error) {
	if validatePassword(password) != nil {
		return "", ErrValidation
	}
	release, err := bootstrapHashSlot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrRateLimited
	}
	defer release()
	hash, err := hashPassword(password)
	if err != nil {
		return "", authError(err)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return hash, nil
}

type enrollmentStart struct {
	snapshot    onboardingSnapshot
	budget      authBudget
	token, hash string
	encrypted   []byte
}

func (s *AuthService) startEnrollmentTx(ctx context.Context, tx *sql.Tx, req AuthRequest, command enrollmentStart, out *SessionTokens) error {
	x, err := s.session(ctx, tx, req, sessionPreauth, true)
	if err != nil {
		return err
	}
	current, err := readOnboarding(ctx, tx, command.token)
	if err != nil {
		return err
	}
	if current.user != command.snapshot.user || current.version != command.snapshot.version {
		return ErrInvalidCredentials
	}
	*out, err = s.issue(ctx, tx, current.user, sessionEnrollment, current.version, false)
	if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `UPDATE public.sessions SET expires_at=LEAST(expires_at,$2) WHERE token_digest=$1 RETURNING expires_at`, digest(out.Token), current.expires).Scan(&out.ExpiresAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.enrollment_attempts(session_id,user_id,token_digest,credential_version,password_hash,pending_ciphertext,key_version,created_at,expires_at) SELECT id,user_id,$2,credential_version,$3,$4,$5,created_at,expires_at FROM public.sessions WHERE token_digest=$1`, digest(out.Token), digest(command.token), command.hash, command.encrypted, s.config.KeyVersion); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.onboarding_tokens SET consumed_at=clock_timestamp() WHERE digest=$1`, digest(command.token)); err != nil {
		return err
	}
	if err = revokeSession(ctx, tx, x.id); err != nil {
		return err
	}
	if err = command.budget.success(ctx, tx); err != nil {
		return err
	}
	return authAudit(ctx, tx, "auth.enrollment_started", current.user, false)
}

type pendingEnrollment struct {
	encrypted []byte
	hash      string
	key       int
}

func (s *AuthService) pendingEnrollment(ctx context.Context, tx *sql.Tx, x Session) (pendingEnrollment, error) {
	var pending pendingEnrollment
	err := tx.QueryRowContext(ctx, `SELECT a.pending_ciphertext,a.key_version,a.password_hash FROM public.enrollment_attempts a JOIN public.onboarding_tokens t ON t.digest=a.token_digest WHERE a.session_id=$1 AND a.user_id=$2 AND a.credential_version=$3 AND t.credential_version=$3 AND a.ended_at IS NULL AND a.expires_at>clock_timestamp() AND t.expires_at>clock_timestamp() AND t.consumed_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.user_roles WHERE user_id=$2)`, x.id, x.user, x.version).Scan(&pending.encrypted, &pending.key, &pending.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return pending, ErrAuthenticationRequired
	}
	if err != nil {
		return pending, err
	}
	if pending.key != s.config.KeyVersion {
		return pending, ErrSecurityUnavailable
	}
	return pending, nil
}

func (s *AuthService) completeEnrollmentTx(ctx context.Context, tx *sql.Tx, req AuthRequest, code string, digests [][]byte, budget authBudget, rejected *bool) error {
	x, err := s.session(ctx, tx, req, sessionEnrollment, true)
	if err != nil {
		return err
	}
	pending, err := s.pendingEnrollment(ctx, tx, x)
	if err != nil {
		return err
	}
	secret, err := decryptSeed(s.config.EncryptionKey, s.config.Site, x.user, pending.key, pending.encrypted)
	if err != nil {
		return err
	}
	counter, err := acceptedCounter(secret, code, x.now, -1)
	if err != nil {
		*rejected = true
		if _, err = tx.ExecContext(ctx, `UPDATE public.sessions SET failed_attempts=failed_attempts+1 WHERE id=$1`, x.id); err != nil {
			return err
		}
		return authAudit(ctx, tx, auditAuthDenied, x.user, true)
	}
	if err = installEnrollment(ctx, tx, x, pending, counter, digests); err != nil {
		return err
	}
	if err = budget.success(ctx, tx); err != nil {
		return err
	}
	return authAudit(ctx, tx, "auth.enrollment_completed", x.user, false)
}

func installEnrollment(ctx context.Context, tx *sql.Tx, x Session, pending pendingEnrollment, counter int64, digests [][]byte) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.mfa_totp(user_id,ciphertext,key_version,confirmed_at,last_counter) VALUES($1,$2,$3,$4,$5)`, x.user, pending.encrypted, pending.key, x.now, counter); err != nil {
		return err
	}
	for _, d := range digests {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.recovery_codes(user_id,digest)VALUES($1,$2)`, x.user, d); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.users SET state='disabled',password_hash=$2,enrollment_completed_at=$3,credential_version=credential_version+1,version=version+1 WHERE id=$1`, x.user, pending.hash, x.now); err != nil {
		return err
	}
	return revokeUser(ctx, tx, x.user)
}
