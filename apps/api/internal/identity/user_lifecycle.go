package identity

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
)

// User pagination shares the installation's bounded account capacity.
const (
	DefaultUserPageLimit = 20
	MaxUserPageLimit     = maxUsers
	MaxUserPageOffset    = maxUsers
)

const (
	opUsersRead     = "users.read"
	opUsersActivate = "users.activate"
	opUsersDisable  = "users.disable"
)

var ErrLastAdminRequired = errors.New("last_admin_required")

// UserPage selects a stable page ordered by normalized login and identity.
type UserPage struct{ Limit, Offset int }

// UserList contains only the administration account projection.
type UserList struct {
	Items      []User
	NextOffset *int
}

func (p UserPage) normalize() (UserPage, error) {
	if p.Limit == 0 {
		p.Limit = DefaultUserPageLimit
	}
	if p.Limit < 1 || p.Limit > MaxUserPageLimit || p.Offset < 0 || p.Offset > MaxUserPageOffset {
		return p, ErrValidation
	}
	return p, nil
}

// ListUsers reads a bounded safe projection using current installation authority.
func (s *AuthService) ListUsers(ctx context.Context, req AuthRequest, page UserPage) (UserList, error) {
	out := UserList{Items: []User{}}
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if err := requirePermission(ctx, tx, x.user, opUsersRead); err != nil {
			return err
		}
		var err error
		page, err = page.normalize()
		if err != nil {
			return err
		}
		if err = checkAccessCaps(ctx, tx); err != nil {
			return err
		}
		out, err = queryUserPage(ctx, tx, page)
		return err
	})
	if err != nil {
		return UserList{}, err
	}
	return out, nil
}

func queryUserPage(ctx context.Context, tx *sql.Tx, page UserPage) (UserList, error) {
	out := UserList{Items: []User{}}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,login_key,state,version FROM public.users ORDER BY login_key,id LIMIT $1 OFFSET $2`, page.Limit+1, page.Offset)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Login, &u.State, &u.Version); err != nil {
			return out, err
		}
		if len(out.Items) == page.Limit {
			next := page.Offset + page.Limit
			out.NextOffset = &next
			break
		}
		out.Items = append(out.Items, u)
	}
	return out, rows.Err()
}

// GetUser returns no factors, credential material or memberships.
func (s *AuthService) GetUser(ctx context.Context, req AuthRequest, id string) (User, error) {
	var out User
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if err := requirePermission(ctx, tx, x.user, opUsersRead); err != nil {
			return err
		}
		if err := checkAccessCaps(ctx, tx); err != nil {
			return err
		}
		var err error
		out, err = findUser(ctx, tx, id)
		return err
	})
	if err != nil {
		return User{}, err
	}
	return out, nil
}

func findUser(ctx context.Context, tx *sql.Tx, id string) (User, error) {
	var out User
	var parsed pgtype.UUID
	if parsed.Scan(id) != nil || !parsed.Valid {
		return out, ErrValidation
	}
	err := tx.QueryRowContext(ctx, `SELECT id::text,login_key,state,version FROM public.users WHERE id=$1`, id).Scan(&out.ID, &out.Login, &out.State, &out.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

// ActivateUser enables only an enrolled disabled account without assigning access.
func (s *AuthService) ActivateUser(ctx context.Context, req AuthRequest, id string, expectedVersion int64) (User, error) {
	return s.changeUserState(ctx, req, id, expectedVersion, true)
}

// DisableUser immediately invalidates every account-bound authentication attempt.
func (s *AuthService) DisableUser(ctx context.Context, req AuthRequest, id string, expectedVersion int64) (User, error) {
	return s.changeUserState(ctx, req, id, expectedVersion, false)
}

func (s *AuthService) changeUserState(ctx context.Context, req AuthRequest, id string, version int64, activate bool) (User, error) {
	permission, event := opUsersDisable, "users.disabled"
	if activate {
		permission, event = opUsersActivate, "users.activated"
	}
	var out User
	err := s.authorizedCommand(ctx, req, []string{permission}, event, func(ctx context.Context, tx *sql.Tx, x Session) error {
		var err error
		out, err = s.applyUserState(ctx, tx, x, userStateChange{id, version, activate, event})
		return err
	})
	if err != nil {
		return User{}, err
	}
	return out, nil
}

type userStateChange struct {
	id       string
	version  int64
	activate bool
	event    string
}

func (s *AuthService) applyUserState(ctx context.Context, tx *sql.Tx, x Session, command userStateChange) (User, error) {
	access, err := loadAccess(ctx, tx)
	if err != nil {
		return User{}, err
	}
	out, err := findUser(ctx, tx, command.id)
	if err != nil {
		return User{}, err
	}
	// Latent roles remain in the affected set even while archived or disabled.
	if !withinEnvelope(access[x.user].active, access[out.ID].latent) {
		return User{}, ErrNotFound
	}
	if err = command.validate(out); err != nil {
		return User{}, err
	}
	if command.activate {
		if err = s.requireEnrolledFactors(ctx, tx, out.ID); err != nil {
			return User{}, err
		}
	}
	if err = writeUserState(ctx, tx, &out, command.activate); err != nil {
		return User{}, err
	}
	if err = s.requireAccessAdmin(ctx, tx, access); err != nil {
		return User{}, err
	}
	err = securityAudit(ctx, tx, command.event, x.user, out.ID, "")
	return out, err
}

func (c userStateChange) validate(user User) error {
	if c.version < 1 {
		return ErrValidation
	}
	if user.Version != c.version {
		return ErrVersionConflict
	}
	if c.activate && user.State != userStateDisabled || !c.activate && user.State != userStateActive {
		return ErrInvalidTransition
	}
	// A full-session actor is already active: activation can never increase its
	// own access, and disabling self is checked against the final admin set.
	return nil
}

func writeUserState(ctx context.Context, tx *sql.Tx, out *User, activate bool) error {
	state := userStateDisabled
	if activate {
		state = userStateActive
	}
	if err := revokeUser(ctx, tx, out.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.onboarding_tokens SET consumed_at=clock_timestamp() WHERE user_id=$1 AND consumed_at IS NULL`, out.ID); err != nil {
		return err
	}
	err := tx.QueryRowContext(ctx, `UPDATE public.users SET state=$2,version=version+1,credential_version=credential_version+1 WHERE id=$1 RETURNING state,version`, out.ID, state).Scan(&out.State, &out.Version)
	return err
}

func (s *AuthService) requireEnrolledFactors(ctx context.Context, tx *sql.Tx, user string) error {
	var ciphertext []byte
	var key int
	var valid bool
	var password string
	err := tx.QueryRowContext(ctx, `SELECT m.ciphertext,COALESCE(m.key_version,0),COALESCE(u.password_hash,''),u.password_hash IS NOT NULL AND u.enrollment_completed_at IS NOT NULL AND m.confirmed_at IS NOT NULL AND EXISTS(SELECT 1 FROM public.recovery_codes c WHERE c.user_id=u.id AND c.consumed_at IS NULL) FROM public.users u LEFT JOIN public.mfa_totp m ON m.user_id=u.id WHERE u.id=$1`, user).Scan(&ciphertext, &key, &password, &valid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidTransition
		}
		return err
	}
	_, _, passwordValid := passwordHashParts(password)
	if !valid || !passwordValid {
		return ErrInvalidTransition
	}
	_, err = decryptSeed(s.config.EncryptionKey, s.config.Site, user, key, ciphertext)
	return err
}
