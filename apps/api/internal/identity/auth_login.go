package identity

import (
	"context"
	"database/sql"
	"errors"
)

const authLoginSQL = `SELECT login_key FROM public.users WHERE id=$1`

type authSnapshot struct {
	bootstrapSnapshot
	budget authBudget
}

func (s *AuthService) password(ctx context.Context, req AuthRequest, login, password, scope string) (authSnapshot, error) {
	key, err := normalizeLogin(login)
	if err != nil {
		key = "!invalid"
	}
	budget, err := s.limitScope(ctx, req, scope, key)
	if err != nil {
		return authSnapshot{}, err
	}
	snap := authSnapshot{budget: budget}
	var state string
	err = s.db.QueryRowContext(ctx, `SELECT id::text,COALESCE(password_hash,''),credential_version,state FROM public.users WHERE login_key=$1`, key).Scan(&snap.id, &snap.hash, &snap.version, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return snap, authError(err)
	}
	hash := snap.hash
	if snap.id == "" || state != "active" || hash == "" {
		hash = s.dummyHash
	}
	valid, err := s.verifyBudgetPassword(ctx, budget, password, hash)
	if err != nil {
		return snap, err
	}
	if !valid || snap.id == "" || state != "active" {
		return snap, s.denied(ctx, snap.id)
	}
	return snap, nil
}

func (s *AuthService) verifyBudgetPassword(ctx context.Context, budget authBudget, password, hash string) (bool, error) {
	release, err := passwordHashSlot(ctx, budget.hashJobs)
	if errors.Is(err, ErrRateLimited) {
		if releaseErr := s.releaseBudget(ctx, budget); releaseErr != nil {
			return false, releaseErr
		}
	}
	if err != nil {
		return false, err
	}
	valid := verifyPassword(password, hash)
	release()
	return valid, ctx.Err()
}
func (s *AuthService) denied(ctx context.Context, user string) error {
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error { return authAudit(ctx, tx, auditAuthDenied, user, true) })
	if err != nil {
		return err
	}
	return ErrInvalidCredentials
}
func currentSnapshot(ctx context.Context, tx *sql.Tx, snap authSnapshot) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.users WHERE id=$1 AND state='active' AND password_hash=$2 AND credential_version=$3)`, snap.id, snap.hash, snap.version).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrInvalidCredentials
	}
	return nil
}
func (s *AuthService) Login(ctx context.Context, req AuthRequest, login, password string) (SessionTokens, error) {
	// Reject forged CSRF before consuming expensive password capacity.
	if err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := s.session(ctx, tx, req, sessionPreauth, true)
		return err
	}); err != nil {
		return SessionTokens{}, err
	}
	snap, err := s.password(ctx, req, login, password, RateCredentials)
	if err != nil {
		return SessionTokens{}, err
	}
	var out SessionTokens
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, sessionPreauth, true)
		if err != nil {
			return err
		}
		if err = currentSnapshot(ctx, tx, snap); err != nil {
			return err
		}
		if err = revokeSession(ctx, tx, x.id); err != nil {
			return err
		}
		out, err = s.issue(ctx, tx, snap.id, sessionLogin, snap.version, false)
		if err != nil {
			return err
		}
		if err = snap.budget.success(ctx, tx); err != nil {
			return err
		}
		return authAudit(ctx, tx, "auth.login_started", snap.id, false)
	})
	if err != nil {
		return SessionTokens{}, err
	}
	return out, nil
}
func (s *AuthService) acceptTOTP(ctx context.Context, tx *sql.Tx, x Session, code string) error {
	var cipher []byte
	var key int
	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT ciphertext,key_version,last_counter FROM public.mfa_totp WHERE user_id=$1 AND confirmed_at IS NOT NULL`, x.user).Scan(&cipher, &key, &last); err != nil {
		return err
	}
	if key != s.config.KeyVersion {
		return ErrSecurityUnavailable
	}
	secret, err := decryptSeed(s.config.EncryptionKey, s.config.Site, x.user, key, cipher)
	if err != nil {
		return err
	}
	counter, err := acceptedCounter(secret, code, x.now, last)
	if err != nil {
		return ErrInvalidCredentials
	}
	_, err = tx.ExecContext(ctx, `UPDATE public.mfa_totp SET last_counter=$2 WHERE user_id=$1`, x.user, counter)
	return err
}
func (s *AuthService) VerifyMFA(ctx context.Context, req AuthRequest, code string) (SessionTokens, error) {
	return s.factor(ctx, req, code, nil)
}

// factorCommand binds factor verification to its admission budget and the
// password snapshot required only by a step-up command.
type factorCommand struct {
	request               AuthRequest
	kind, operation, code string
	snapshot              *authSnapshot
	budget                authBudget
}

func (s *AuthService) factor(ctx context.Context, req AuthRequest, code string, snap *authSnapshot) (SessionTokens, error) {
	kind := sessionLogin
	op := "auth.login_completed"
	if snap != nil {
		kind = sessionFull
		op = "auth.step_up"
	}
	var user string
	if err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, kind, true)
		user = x.user
		return err
	}); err != nil {
		return SessionTokens{}, err
	}
	var account string
	if err := s.db.QueryRowContext(ctx, authLoginSQL, user).Scan(&account); err != nil {
		return SessionTokens{}, authError(err)
	}
	var budget authBudget
	var err error
	if snap != nil {
		budget = snap.budget
	} else {
		budget, err = s.limit(ctx, req, account)
		if err != nil {
			return SessionTokens{}, err
		}
	}
	command := factorCommand{request: req, kind: kind, operation: op, code: code, snapshot: snap, budget: budget}
	var out SessionTokens
	var rejected bool
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.factorTx(ctx, tx, command, &out, &rejected)
	})
	if err != nil {
		return SessionTokens{}, err
	}
	if rejected {
		return SessionTokens{}, ErrInvalidCredentials
	}
	return out, nil
}
func checkFactorSnapshot(ctx context.Context, tx *sql.Tx, x Session, snap *authSnapshot) error {
	if snap == nil {
		return nil
	}
	if x.user != snap.id {
		return ErrInvalidCredentials
	}
	return currentSnapshot(ctx, tx, *snap)
}
func (s *AuthService) factorTx(ctx context.Context, tx *sql.Tx, command factorCommand, out *SessionTokens, rejected *bool) error {
	x, err := s.session(ctx, tx, command.request, command.kind, true)
	if err != nil {
		return err
	}
	if err = checkFactorSnapshot(ctx, tx, x, command.snapshot); err != nil {
		return err
	}
	if err = s.acceptTOTP(ctx, tx, x, command.code); errors.Is(err, ErrInvalidCredentials) {
		*rejected = true
		if command.kind == sessionLogin {
			if _, err = tx.ExecContext(ctx, `UPDATE public.sessions SET failed_attempts=failed_attempts+1 WHERE id=$1`, x.id); err != nil {
				return err
			}
		}
		return authAudit(ctx, tx, auditAuthDenied, x.user, true)
	} else if err != nil {
		return err
	}
	return s.rotateFull(ctx, tx, x, command.snapshot != nil, command.budget, command.operation, out)
}
func (s *AuthService) rotateFull(ctx context.Context, tx *sql.Tx, x Session, step bool, budget authBudget, op string, out *SessionTokens) error {
	if err := revokeSession(ctx, tx, x.id); err != nil {
		return err
	}
	var err error
	// Step-up rotates credentials without extending the original session lifetime.
	if step {
		lifetime := sessionLifetime{
			idleTimeoutMinutes: x.idleTimeoutMinutes,
			createdAt:          sql.NullTime{Time: x.created, Valid: true},
			expiresAt:          sql.NullTime{Time: x.expires, Valid: true},
		}
		*out, err = issueSession(ctx, tx, x.user, sessionFull, x.version, true, lifetime)
	} else {
		*out, err = s.issue(ctx, tx, x.user, sessionFull, x.version, true)
	}
	if err != nil {
		return err
	}
	if err = budget.success(ctx, tx); err != nil {
		return err
	}
	return authAudit(ctx, tx, op, x.user, false)
}
func (s *AuthService) StepUp(ctx context.Context, req AuthRequest, password, code string) (SessionTokens, error) {
	var login string
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, sessionFull, true)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, authLoginSQL, x.user).Scan(&login)
	})
	if err != nil {
		return SessionTokens{}, err
	}
	snap, err := s.password(ctx, req, login, password, RateStepUp)
	if err != nil {
		return SessionTokens{}, err
	}
	return s.factor(ctx, req, code, &snap)
}
func (s *AuthService) RotateCSRF(ctx context.Context, token, kind string) (SessionTokens, error) {
	var out SessionTokens
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, AuthRequest{Token: token}, kind, false)
		if err != nil {
			return err
		}
		csrf, err := bearer()
		if err != nil {
			return err
		}
		out = SessionTokens{Token: token, CSRF: csrf, Kind: kind}
		return tx.QueryRowContext(ctx, `UPDATE public.sessions SET csrf_digest=$2,last_seen=$3 WHERE id=$1 RETURNING expires_at`, x.id, digest(csrf), x.now).Scan(&out.ExpiresAt)
	})
	if err != nil {
		return SessionTokens{}, err
	}
	return out, nil
}
func (s *AuthService) Logout(ctx context.Context, req AuthRequest, kind string) error {
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, kind, true)
		if err != nil {
			return err
		}
		if kind == sessionRecovery {
			if err = s.endRecovery(ctx, tx, x, "mfa.recovery_cancelled"); err != nil {
				return err
			}
		}
		if kind == sessionEnrollment {
			if err = endEnrollment(ctx, tx, x); err != nil {
				return err
			}
		}
		if err = revokeSession(ctx, tx, x.id); err != nil {
			return err
		}
		return authAudit(ctx, tx, "auth.logout", x.user, false)
	})
}
func (s *AuthService) RevokeAll(ctx context.Context, req AuthRequest) error {
	return s.accountCredentialCommand(ctx, req, sessionsRevokedEvent, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if err := revokeUser(ctx, tx, x.user); err != nil {
			return err
		}
		return securityAudit(ctx, tx, sessionsRevokedEvent, x.user, x.user, "")
	})
}
func revokeUser(ctx context.Context, tx *sql.Tx, user string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.enrollment_attempts SET ended_at=clock_timestamp() WHERE user_id=$1 AND ended_at IS NULL`, user); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.recovery_attempts SET ended_at=clock_timestamp() WHERE user_id=$1 AND ended_at IS NULL`, user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE public.sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`, user)
	return err
}

// NewPreauthFor bounds unauthenticated issuance using the trusted client address.
func (s *AuthService) NewPreauthFor(ctx context.Context, req AuthRequest) (SessionTokens, error) {
	if !req.ClientIP.IsValid() {
		return SessionTokens{}, ErrInvalidCredentials
	}
	var out SessionTokens
	var throttle error
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := cleanupAuth(ctx, tx); err != nil {
			return err
		}
		_, err := reserveAuthBudget(ctx, tx, RatePreauth, "", req.ClientIP.Unmap().String(), s.config.Site)
		if errors.Is(err, ErrRateLimited) {
			throttle = err
			return nil
		}
		if err != nil {
			return err
		}
		out, err = s.issue(ctx, tx, "", sessionPreauth, 0, false)
		return err
	})
	if err != nil {
		return SessionTokens{}, err
	}
	return out, throttle
}

func cleanupAuth(ctx context.Context, tx *sql.Tx) error {
	// Historical full/recovery sessions and immutable audit have separate retention.
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.sessions WHERE id IN (SELECT id FROM public.sessions WHERE kind IN ('preauth','login') AND (expires_at<=clock_timestamp() OR revoked_at IS NOT NULL) ORDER BY expires_at LIMIT 100)`); err != nil {
		return err
	}
	return nil
}
