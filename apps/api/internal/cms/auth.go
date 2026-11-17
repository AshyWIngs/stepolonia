package cms

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/netip"
	"time"
)

const (
	sessionFull       = "full"
	sessionLogin      = "login"
	sessionPreauth    = "preauth"
	sessionRecovery   = "recovery"
	sessionEnrollment = "enrollment"
)

var (
	ErrInvalidCredentials     = errors.New("invalid_credentials")
	ErrAuthenticationRequired = errors.New("authentication_required")
	ErrCSRF                   = errors.New("csrf_failed")
	ErrRateLimited            = errors.New("rate_limited")
	ErrSecurityUnavailable    = errors.New("security_unavailable")
	ErrPermissionDenied       = errors.New("permission_denied")
	ErrStepUpRequired         = errors.New("step_up_required")
	ErrVersionConflict        = errors.New("version_conflict")
	ErrInvalidTransition      = errors.New("invalid_transition")
	ErrNotFound               = errors.New("not_found")
	ErrValidation             = errors.New("validation_failed")
)

// AuthConfig binds credentials to one installation and external encryption key.
type AuthConfig = BootstrapConfig

type AuthService struct {
	db     *sql.DB
	config AuthConfig
	dummy  string
}

// AuthRequest contains bearer credentials and a transport-verified client address.
// The adapter must validate canonical HTTPS Origin before calling unsafe commands.
type AuthRequest struct {
	Token, CSRF string
	ClientIP    netip.Addr
	// RequestID is a server-generated UUID, never a caller-selected identity.
	RequestID string
}

// SessionTokens contains secrets returned only after their transaction commits.
type SessionTokens struct {
	Token, CSRF, Kind string
	ExpiresAt         time.Time
}

// Session is a database-verified identity; it cannot be constructed by callers.
// It is valid only in the transaction supplied to WithSession.
type Session struct {
	id, user, kind        string
	version               int64
	now, created, expires time.Time
	stepUp                sql.NullTime
}

func (s Session) UserID() string { return s.user }
func (s Session) Kind() string   { return s.kind }
func (s Session) FreshStepUp() bool {
	return s.kind == sessionFull && s.stepUp.Valid && !s.stepUp.Time.After(s.now) && s.now.Sub(s.stepUp.Time) <= 5*time.Minute
}

func NewAuthService(db *sql.DB, config AuthConfig) (*AuthService, error) {
	b, err := NewBootstrapService(db, config)
	if err != nil {
		return nil, err
	}
	// Fixed valid parameters give nonexistent identities the same expensive work.
	dummy := "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	return &AuthService{db: db, config: b.config, dummy: dummy}, nil
}
func digest(value string) []byte { d := sha256.Sum256([]byte(value)); return d[:] }
func bearer() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func validBearer(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32 && len(value) == 43
}
func authError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrInvalidCredentials, ErrAuthenticationRequired, ErrCSRF, ErrRateLimited, ErrPermissionDenied, ErrStepUpRequired, ErrVersionConflict, ErrInvalidTransition, ErrNotFound, ErrValidation, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return err
		}
	}
	return errors.Join(ErrSecurityUnavailable, err)
}
func (s *AuthService) transaction(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	return authError(WithWriteMutex(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
		var bound bool
		if err := tx.QueryRowContext(ctx, `SELECT site=$1 AND bootstrap_completed FROM public.security_state WHERE id=1`, s.config.Site).Scan(&bound); err != nil {
			return err
		}
		if !bound {
			return ErrSecurityUnavailable
		}
		return fn(ctx, tx)
	}))
}

const auditAuthDenied = "auth.denied"

func authAudit(ctx context.Context, tx *sql.Tx, op, user string, denied bool) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	result, reason := "success", ""
	if denied {
		result, reason = "denied", "invalid_credentials"
	}
	var actor any
	if user != "" {
		actor = user
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.security_audit(id,operation,actor_id,target_id,result,reason,request_id)VALUES($1,$2,$3,$3,$4,$5,$6)`, id, op, actor, result, reason, ctx.Value(auditRequestKey{}))
	return err
}
func (s *AuthService) issue(ctx context.Context, tx *sql.Tx, user, kind string, version int64, step bool) (SessionTokens, error) {
	var out SessionTokens
	var err error
	out.Token, err = bearer()
	if err != nil {
		return out, err
	}
	out.CSRF, err = bearer()
	if err != nil {
		return out, err
	}
	id, err := randomID()
	if err != nil {
		return out, err
	}
	ttl := "5 minutes"
	if kind == sessionFull {
		ttl = "12 hours"
	}
	if kind == sessionRecovery || kind == sessionEnrollment {
		ttl = "10 minutes"
	}
	var uid, cv any
	if user != "" {
		uid, cv = user, version
	}
	out.Kind = kind
	err = tx.QueryRowContext(ctx, `INSERT INTO public.sessions(id,token_digest,csrf_digest,user_id,kind,credential_version,created_at,last_seen,expires_at,step_up_at) SELECT $1,$2,$3,$4,$5,$6,n,n,n+$7::interval,CASE WHEN $8 THEN n END FROM (SELECT clock_timestamp() n)t RETURNING expires_at`, id, digest(out.Token), digest(out.CSRF), uid, kind, cv, ttl, step).Scan(&out.ExpiresAt)
	return out, err
}
func (s *AuthService) NewPreauth(ctx context.Context) (SessionTokens, error) {
	var out SessionTokens
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = s.issue(ctx, tx, "", sessionPreauth, 0, false)
		return err
	})
	if err != nil {
		return SessionTokens{}, err
	}
	return out, nil
}
func (s *AuthService) session(ctx context.Context, tx *sql.Tx, req AuthRequest, kind string, csrf bool) (Session, error) {
	var x Session
	var expected []byte
	if !validBearer(req.Token) {
		return x, ErrAuthenticationRequired
	}
	err := tx.QueryRowContext(ctx, `SELECT s.id::text,COALESCE(s.user_id::text,''),s.kind,COALESCE(s.credential_version,0),s.step_up_at,s.csrf_digest,clock_timestamp(),s.created_at,s.expires_at FROM public.sessions s LEFT JOIN public.users u ON u.id=s.user_id WHERE s.token_digest=$1 AND s.kind=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND (s.kind<>'full' OR s.last_seen>clock_timestamp()-interval '30 minutes') AND (s.kind='preauth' OR (u.credential_version=s.credential_version AND ((s.kind='enrollment' AND u.state='pending' AND u.enrollment_completed_at IS NULL) OR (s.kind<>'enrollment' AND u.state='active')))) AND s.failed_attempts<5`, digest(req.Token), kind).Scan(&x.id, &x.user, &x.kind, &x.version, &x.stepUp, &expected, &x.now, &x.created, &x.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrAuthenticationRequired
	}
	if err != nil {
		return x, err
	}
	if csrf && (!validBearer(req.CSRF) || subtle.ConstantTimeCompare(expected, digest(req.CSRF)) != 1) {
		return Session{}, ErrCSRF
	}
	return x, nil
}

// WithSession serializes a full-session protected operation with revocation.
// The callback must not retain the transaction or identity beyond its return.
func (s *AuthService) WithSession(ctx context.Context, req AuthRequest, unsafe bool, fn func(context.Context, *sql.Tx, Session) error) error {
	if fn == nil {
		return ErrSecurityUnavailable
	}
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		x, err := s.session(ctx, tx, req, sessionFull, unsafe)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE public.sessions SET last_seen=$2 WHERE id=$1`, x.id, x.now); err != nil {
			return err
		}
		return fn(ctx, tx, x)
	})
}
func revokeSession(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE public.sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL`, id)
	return err
}

// CheckReady verifies installation binding and all confirmed MFA keys before
// serving authentication. It performs no writes and accepts a startup context.
func (s *AuthService) CheckReady(ctx context.Context) error {
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT user_id::text,ciphertext,key_version FROM public.mfa_totp WHERE confirmed_at IS NOT NULL`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var user string
			var ciphertext []byte
			var version int
			if err = rows.Scan(&user, &ciphertext, &version); err != nil {
				return err
			}
			if version != s.config.KeyVersion {
				return ErrSecurityUnavailable
			}
			if _, err = decryptSeed(s.config.EncryptionKey, s.config.Site, user, version, ciphertext); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}
