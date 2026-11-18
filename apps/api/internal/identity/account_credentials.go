package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
)

const (
	passwordChangedEvent   = "auth.password_changed"
	factorPreparationEvent = "mfa.replacement_started"
	factorReplacedEvent    = "mfa.replaced"
	codesRegeneratedEvent  = "mfa.recovery_codes_regenerated"
)

type FactorPreparation struct {
	Secret, ReplacementToken string
}

func (s *AuthService) accountCredentialCommand(ctx context.Context, req AuthRequest, event string, command func(context.Context, *sql.Tx, Session) error) error {
	return s.auditedCommand(ctx, req, event, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if !session.FreshStepUp() {
			return ErrStepUpRequired
		}
		return command(ctx, tx, session)
	})
}

func (s *AuthService) credentialBudget(ctx context.Context, req AuthRequest, event string) (authBudget, error) {
	var id string
	err := s.WithSession(ctx, req, true, func(_ context.Context, _ *sql.Tx, session Session) error {
		if !session.FreshStepUp() {
			return ErrStepUpRequired
		}
		id = session.user
		return nil
	})
	if err != nil {
		return authBudget{}, err
	}
	return s.limit(ctx, req, event+":"+id)
}

func (s *AuthService) ChangeAccountPassword(ctx context.Context, req AuthRequest, password string) error {
	budget, err := s.credentialBudget(ctx, req, passwordChangedEvent)
	if err != nil {
		return err
	}
	if validatePassword(password) != nil {
		return ErrValidation
	}
	hash, err := bootstrapHash(ctx, password)
	if err != nil {
		return authError(err)
	}
	return s.accountCredentialCommand(ctx, req, passwordChangedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if _, err := tx.ExecContext(ctx, `UPDATE public.users SET password_hash=$2 WHERE id=$1`, session.user, hash); err != nil {
			return err
		}
		if err := budget.success(ctx, tx); err != nil {
			return err
		}
		return commitCredentialChange(ctx, tx, session, passwordChangedEvent)
	})
}

func (s *AuthService) PrepareFactorReplacement(ctx context.Context, req AuthRequest) (FactorPreparation, error) {
	if _, err := s.credentialBudget(ctx, req, factorPreparationEvent); err != nil {
		return FactorPreparation{}, err
	}
	var out FactorPreparation
	err := s.accountCredentialCommand(ctx, req, factorPreparationEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		seed, err := newSecret()
		if err != nil {
			return err
		}
		token, err := encryptSeed(s.config.EncryptionKey, s.config.Site, replacementBinding(session), s.config.KeyVersion, seed)
		if err != nil {
			return err
		}
		out = FactorPreparation{Secret: seed, ReplacementToken: base64.RawURLEncoding.EncodeToString(token)}
		return securityAudit(ctx, tx, factorPreparationEvent, session.user, session.user, "")
	})
	if err != nil {
		return FactorPreparation{}, err
	}
	return out, nil
}

func replacementBinding(session Session) string {
	return "replacement/" + session.user + "/" + session.id
}

func (s *AuthService) CompleteFactorReplacement(ctx context.Context, req AuthRequest, token, code string) ([]string, error) {
	budget, err := s.credentialBudget(ctx, req, factorReplacedEvent)
	if err != nil {
		return nil, err
	}
	if len(token) != 80 || len(code) != 6 {
		return nil, ErrValidation
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	codes, digests, err := recoveryCodes()
	if err != nil {
		return nil, authError(err)
	}
	err = s.accountCredentialCommand(ctx, req, factorReplacedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := s.confirmReplacementFactor(ctx, tx, session, raw, code); err != nil {
			return err
		}
		if err := storeReplacementCodes(ctx, tx, session, digests); err != nil {
			return err
		}
		if err := budget.success(ctx, tx); err != nil {
			return err
		}
		return commitCredentialChange(ctx, tx, session, factorReplacedEvent)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *AuthService) confirmReplacementFactor(ctx context.Context, tx *sql.Tx, session Session, raw []byte, code string) error {
	seed, err := decryptSeed(s.config.EncryptionKey, s.config.Site, replacementBinding(session), s.config.KeyVersion, raw)
	if err != nil {
		return ErrInvalidCredentials
	}
	counter, err := acceptedCounter(seed, code, session.now, -1)
	if err != nil {
		return ErrInvalidCredentials
	}
	ciphertext, err := encryptSeed(s.config.EncryptionKey, s.config.Site, session.user, s.config.KeyVersion, seed)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE public.mfa_totp SET ciphertext=$2,key_version=$3,confirmed_at=$4,last_counter=$5
 WHERE user_id=$1 AND confirmed_at IS NOT NULL`, session.user, ciphertext, s.config.KeyVersion, session.now, counter)
	if err != nil {
		return err
	}
	if count, err := res.RowsAffected(); err != nil || count != 1 {
		return ErrSecurityUnavailable
	}
	return nil
}

func (s *AuthService) RegenerateRecoveryCodes(ctx context.Context, req AuthRequest) ([]string, error) {
	codes, digests, err := recoveryCodes()
	if err != nil {
		return nil, authError(err)
	}
	err = s.accountCredentialCommand(ctx, req, codesRegeneratedEvent, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := storeReplacementCodes(ctx, tx, session, digests); err != nil {
			return err
		}
		return commitCredentialChange(ctx, tx, session, codesRegeneratedEvent)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func storeReplacementCodes(ctx context.Context, tx *sql.Tx, session Session, digests [][]byte) error {
	if _, err := tx.ExecContext(ctx, `UPDATE public.recovery_codes SET consumed_at=$2 WHERE user_id=$1 AND consumed_at IS NULL`, session.user, session.now); err != nil {
		return err
	}
	for _, digest := range digests {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.recovery_codes(user_id,digest) VALUES($1,$2)`, session.user, digest); err != nil {
			return err
		}
	}
	return nil
}

func commitCredentialChange(ctx context.Context, tx *sql.Tx, session Session, event string) error {
	if err := advanceCredentialVersion(ctx, tx, session.user); err != nil {
		return err
	}
	if err := revokeUser(ctx, tx, session.user); err != nil {
		return err
	}
	if err := checkRecoveryAdmin(ctx, tx, session.user); err != nil {
		return err
	}
	return securityAudit(ctx, tx, event, session.user, session.user, "")
}
