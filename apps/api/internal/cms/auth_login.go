package cms

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type authBudget struct {
	at                time.Time
	account, ip, site string
}
type authSnapshot struct {
	bootstrapSnapshot
	budget authBudget
}

// limit reserves persistent budgets before expensive work. Success releases the
// exact reservation; failed or interrupted commands retain it for 15 minutes.
func (s *AuthService) limit(ctx context.Context, req AuthRequest, account string) (authBudget, error) {
	b := authBudget{account: account, ip: req.ClientIP.Unmap().String(), site: s.config.Site}
	if !req.ClientIP.IsValid() {
		return b, ErrInvalidCredentials
	}
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return b.reserve(ctx, tx)
	})
	return b, err
}
func (b *authBudget) reserve(ctx context.Context, tx *sql.Tx) error {
	keys := []struct {
		kind, key string
		max       int
	}{{"account", b.account, 5}, {"ip", b.ip, 30}, {"site", b.site, 100}}
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&b.at); err != nil {
		return err
	}
	for _, k := range keys {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(count),0) FROM public.auth_attempts WHERE kind=$1 AND key_digest=$2 AND bucket_start>$3::timestamptz-interval '15 minutes'`, k.kind, digest(k.key), b.at).Scan(&n); err != nil {
			return err
		}
		if n >= k.max {
			return ErrRateLimited
		}
	}
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.auth_attempts(bucket_start,kind,key_digest,count) VALUES($3,$1,$2,1)`, k.kind, digest(k.key), b.at); err != nil {
			return err
		}
	}
	return nil
}
func (b authBudget) success(ctx context.Context, tx *sql.Tx) error {
	for _, k := range []struct{ kind, key string }{{"account", b.account}, {"ip", b.ip}, {"site", b.site}} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM public.auth_attempts WHERE bucket_start=$1 AND kind=$2 AND key_digest=$3`, b.at, k.kind, digest(k.key)); err != nil {
			return err
		}
	}
	return nil
}
func (s *AuthService) password(ctx context.Context, req AuthRequest, login, password string) (authSnapshot, error) {
	key, err := normalizeLogin(login)
	if err != nil {
		key = "!invalid"
	}
	budget, err := s.limit(ctx, req, key)
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
		hash = s.dummy
	}
	release, err := bootstrapHashSlot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return snap, ctx.Err()
		}
		return snap, ErrRateLimited
	}
	valid := verifyPassword(password, hash)
	release()
	if ctx.Err() != nil {
		return snap, ctx.Err()
	}
	if !valid || snap.id == "" || state != "active" {
		return snap, s.denied(ctx, snap.id)
	}
	return snap, nil
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
	snap, err := s.password(ctx, req, login, password)
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
	var out SessionTokens
	var rejected bool
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.factorTx(ctx, tx, req, kind, op, code, snap, budget, &out, &rejected)
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
func (s *AuthService) factorTx(ctx context.Context, tx *sql.Tx, req AuthRequest, kind, op, code string, snap *authSnapshot, budget authBudget, out *SessionTokens, rejected *bool) error {
	x, err := s.session(ctx, tx, req, kind, true)
	if err != nil {
		return err
	}
	if err = checkFactorSnapshot(ctx, tx, x, snap); err != nil {
		return err
	}
	if err = s.acceptTOTP(ctx, tx, x, code); errors.Is(err, ErrInvalidCredentials) {
		*rejected = true
		if kind == sessionLogin {
			if _, err = tx.ExecContext(ctx, `UPDATE public.sessions SET failed_attempts=failed_attempts+1 WHERE id=$1`, x.id); err != nil {
				return err
			}
		}
		return authAudit(ctx, tx, auditAuthDenied, x.user, true)
	} else if err != nil {
		return err
	}
	return s.rotateFull(ctx, tx, x, snap != nil, budget, op, out)
}
func (s *AuthService) rotateFull(ctx context.Context, tx *sql.Tx, x Session, step bool, budget authBudget, op string, out *SessionTokens) error {
	if err := revokeSession(ctx, tx, x.id); err != nil {
		return err
	}
	var err error
	*out, err = s.issue(ctx, tx, x.user, sessionFull, x.version, true)
	if err != nil {
		return err
	}
	if step { // Rotation must not restart the original absolute lifetime.
		if _, err = tx.ExecContext(ctx, `UPDATE public.sessions SET created_at=$2,expires_at=$3 WHERE token_digest=$1`, digest(out.Token), x.created, x.expires); err != nil {
			return err
		}
		out.ExpiresAt = x.expires
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
	snap, err := s.password(ctx, req, login, password)
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
		if err = revokeSession(ctx, tx, x.id); err != nil {
			return err
		}
		return authAudit(ctx, tx, "auth.logout", x.user, false)
	})
}
func (s *AuthService) RevokeAll(ctx context.Context, req AuthRequest) error {
	return s.WithSession(ctx, req, true, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if !x.FreshStepUp() {
			return ErrAuthenticationRequired
		}
		if err := revokeUser(ctx, tx, x.user); err != nil {
			return err
		}
		return authAudit(ctx, tx, "auth.sessions_revoked", x.user, false)
	})
}
func revokeUser(ctx context.Context, tx *sql.Tx, user string) error {
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
	var limited bool
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := cleanupAuth(ctx, tx); err != nil {
			return err
		}
		return s.preauthTx(ctx, tx, req, &out, &limited)
	})
	if err != nil {
		return SessionTokens{}, err
	}
	if limited {
		return SessionTokens{}, ErrRateLimited
	}
	return out, nil
}

func cleanupAuth(ctx context.Context, tx *sql.Tx) error {
	// Each maintenance batch is bounded; historical full/recovery sessions and
	// immutable audit remain under the separately defined retention policy.
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.auth_attempts WHERE (bucket_start,kind,key_digest) IN (SELECT bucket_start,kind,key_digest FROM public.auth_attempts WHERE bucket_start<=clock_timestamp()-interval '15 minutes' ORDER BY bucket_start LIMIT 1000)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.sessions WHERE id IN (SELECT id FROM public.sessions WHERE kind IN ('preauth','login') AND (expires_at<=clock_timestamp() OR revoked_at IS NOT NULL) ORDER BY expires_at LIMIT 100)`); err != nil {
		return err
	}
	return nil
}

func (s *AuthService) preauthTx(ctx context.Context, tx *sql.Tx, req AuthRequest, out *SessionTokens, limited *bool) error {
	keys := []struct {
		kind, key string
		max       int
	}{{"preauth_ip", req.ClientIP.Unmap().String(), 30}, {"preauth_site", s.config.Site, 100}}
	for _, k := range keys {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(count),0) FROM public.auth_attempts WHERE kind=$1 AND key_digest=$2 AND bucket_start>clock_timestamp()-interval '15 minutes'`, k.kind, digest(k.key)).Scan(&n); err != nil {
			return err
		}
		if n >= k.max {
			*limited = true
			return nil
		}
	}
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.auth_attempts(bucket_start,kind,key_digest,count) VALUES(clock_timestamp(),$1,$2,1)`, k.kind, digest(k.key)); err != nil {
			return err
		}
	}
	var err error
	*out, err = s.issue(ctx, tx, "", sessionPreauth, 0, false)
	return err
}
