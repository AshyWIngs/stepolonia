package identity

import (
	"context"
	"database/sql"
	"errors"
)

// RecoveryEnrollment exposes only the caller's reserved pending seed.
type RecoveryEnrollment struct {
	SessionTokens
	Secret string
}

func (s *AuthService) Recover(ctx context.Context, req AuthRequest, login, password, code string) (RecoveryEnrollment, error) {
	var out RecoveryEnrollment
	if err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := s.session(ctx, tx, req, sessionPreauth, true)
		return err
	}); err != nil {
		return out, err
	}
	snap, err := s.password(ctx, req, login, password)
	if err != nil {
		return out, err
	}
	secret, err := newSecret()
	if err != nil {
		return out, authError(err)
	}
	encrypted, err := encryptSeed(s.config.EncryptionKey, s.config.Site, snap.id, s.config.KeyVersion, secret)
	if err != nil {
		return out, authError(err)
	}
	// Expiry is committed independently so a failed restart cannot resurrect an
	// expired reservation through rollback of the new attempt.
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.expireRecoveries(ctx, tx, snap)
	})
	if err != nil {
		return out, err
	}
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.startRecoveryTx(ctx, tx, req, snap, code, encrypted, &out.SessionTokens)
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			err = s.denied(ctx, snap.id)
		}
		return RecoveryEnrollment{}, err
	}
	out.Secret = secret
	return out, nil
}
func (s *AuthService) endRecovery(ctx context.Context, tx *sql.Tx, x Session, op string) error {
	result, err := tx.ExecContext(ctx, `UPDATE public.recovery_attempts SET ended_at=clock_timestamp() WHERE session_id=$1 AND ended_at IS NULL`, x.id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAuthenticationRequired
	}
	return authAudit(ctx, tx, op, x.user, false)
}
func (s *AuthService) CancelRecovery(ctx context.Context, req AuthRequest) error {
	return s.Logout(ctx, req, sessionRecovery)
}
func (s *AuthService) CompleteRecovery(ctx context.Context, req AuthRequest, code string) ([]string, error) {
	var login string
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, sessionRecovery, true)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, authLoginSQL, x.user).Scan(&login)
	})
	if err != nil {
		return nil, err
	}
	budget, err := s.limit(ctx, req, login)
	if err != nil {
		return nil, err
	}
	codes, digests, err := recoveryCodes()
	if err != nil {
		return nil, authError(err)
	}
	var rejected bool
	err = s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return s.completeRecoveryTx(ctx, tx, req, code, digests, budget, &rejected)
	})
	if err != nil {
		return nil, err
	}
	if rejected {
		return nil, ErrInvalidCredentials
	}
	return codes, nil
}
func checkRecoveryAdmin(ctx context.Context, tx *sql.Tx, user string) error {
	var usable bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.users u JOIN public.mfa_totp m ON m.user_id=u.id WHERE u.id=$1 AND u.state='active' AND u.password_hash IS NOT NULL AND m.confirmed_at IS NOT NULL AND EXISTS(SELECT 1 FROM public.recovery_codes c WHERE c.user_id=u.id AND c.consumed_at IS NULL))`, user).Scan(&usable)
	if err != nil {
		return err
	}
	if !usable {
		return ErrSecurityUnavailable
	}
	return nil
}

func (s *AuthService) expireRecoveries(ctx context.Context, tx *sql.Tx, snap authSnapshot) error {
	if err := currentSnapshot(ctx, tx, snap); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT session_id::text FROM public.recovery_attempts WHERE user_id=$1 AND ended_at IS NULL AND expires_at<=clock_timestamp()`, snap.id)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.endRecovery(ctx, tx, Session{id: id, user: snap.id}, "mfa.recovery_expired"); err != nil {
			return err
		}
		if err = revokeSession(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthService) startRecoveryTx(ctx context.Context, tx *sql.Tx, req AuthRequest, snap authSnapshot, code string, encrypted []byte, out *SessionTokens) error {
	x, err := s.session(ctx, tx, req, sessionPreauth, true)
	if err != nil {
		return err
	}
	if err = currentSnapshot(ctx, tx, snap); err != nil {
		return err
	}
	var available bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.recovery_codes WHERE user_id=$1 AND digest=$2 AND consumed_at IS NULL) AND NOT EXISTS(SELECT 1 FROM public.recovery_attempts WHERE user_id=$1 AND ended_at IS NULL)`, snap.id, digest(code)).Scan(&available); err != nil {
		return err
	}
	if !available {
		return ErrInvalidCredentials
	}
	*out, err = s.issue(ctx, tx, snap.id, sessionRecovery, snap.version, false)
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.recovery_attempts(id,session_id,user_id,code_digest,credential_version,pending_ciphertext,key_version,created_at,expires_at) SELECT $1,id,$3,$4,$5,$6,$7,created_at,expires_at FROM public.sessions WHERE token_digest=$2`, id, digest(out.Token), snap.id, digest(code), snap.version, encrypted, s.config.KeyVersion); err != nil {
		return err
	}
	if err = revokeSession(ctx, tx, x.id); err != nil {
		return err
	}
	if err = snap.budget.success(ctx, tx); err != nil {
		return err
	}
	return authAudit(ctx, tx, "mfa.recovery_started", snap.id, false)
}

func (s *AuthService) completeRecoveryTx(ctx context.Context, tx *sql.Tx, req AuthRequest, code string, digests [][]byte, budget authBudget, rejected *bool) error {
	x, err := s.session(ctx, tx, req, sessionRecovery, true)
	if err != nil {
		return err
	}
	var encrypted []byte
	var key int
	err = tx.QueryRowContext(ctx, `SELECT a.pending_ciphertext,a.key_version FROM public.recovery_attempts a JOIN public.recovery_codes c ON c.user_id=a.user_id AND c.digest=a.code_digest WHERE a.session_id=$1 AND a.user_id=$2 AND a.credential_version=$3 AND a.ended_at IS NULL AND a.expires_at>clock_timestamp() AND c.consumed_at IS NULL`, x.id, x.user, x.version).Scan(&encrypted, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthenticationRequired
	}
	if err != nil {
		return err
	}
	if key != s.config.KeyVersion {
		return ErrSecurityUnavailable
	}
	secret, err := decryptSeed(s.config.EncryptionKey, s.config.Site, x.user, key, encrypted)
	if err != nil {
		return err
	}
	counter, err := acceptedCounter(secret, code, x.now, -1)
	if err != nil {
		*rejected = true
		return authAudit(ctx, tx, auditAuthDenied, x.user, true)
	}
	completion := recoveryCompletion{
		ciphertext: encrypted, keyVersion: key, acceptedCounter: counter,
		codeDigests: digests, budget: budget,
	}
	return s.replaceRecoveryFactors(ctx, tx, x, completion)
}

// recoveryCompletion contains the confirmed replacement factors and the
// reservation released only when the entire recovery transaction commits.
type recoveryCompletion struct {
	ciphertext      []byte
	keyVersion      int
	acceptedCounter int64
	codeDigests     [][]byte
	budget          authBudget
}

func (s *AuthService) replaceRecoveryFactors(ctx context.Context, tx *sql.Tx, x Session, completion recoveryCompletion) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.mfa_totp SET ciphertext=$2,key_version=$3,confirmed_at=$4,last_counter=$5 WHERE user_id=$1`, x.user, completion.ciphertext, completion.keyVersion, x.now, completion.acceptedCounter); err != nil {
		return err
	}
	if err := storeReplacementCodes(ctx, tx, x, completion.codeDigests); err != nil {
		return err
	}
	if err := advanceCredentialVersion(ctx, tx, x.user); err != nil {
		return err
	}
	if err := revokeUser(ctx, tx, x.user); err != nil {
		return err
	}
	// Account state and grants remain unchanged; factor replacement must preserve
	// the effective-admin predicate against the newly available factor set.
	if err := checkRecoveryAdmin(ctx, tx, x.user); err != nil {
		return err
	}
	if err := completion.budget.success(ctx, tx); err != nil {
		return err
	}
	return authAudit(ctx, tx, "mfa.recovery_completed", x.user, false)
}
