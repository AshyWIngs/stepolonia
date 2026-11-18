package cms

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

const opRolesAssign = "roles.assign"

const userRolesReplacedEvent = "users.roles_replaced"

// ReplaceUserRoles replaces the full role set and advances the user's version.
func (s *AuthService) ReplaceUserRoles(ctx context.Context, req AuthRequest, id string, expectedVersion int64, roleIDs []string) (User, error) {
	var out User
	err := s.authorizedCommand(ctx, req, []string{opRolesAssign}, userRolesReplacedEvent, func(ctx context.Context, tx *sql.Tx, x Session) error {
		var err error
		out, err = s.replaceUserRoles(ctx, tx, x, userRoleReplacement{id, expectedVersion, roleIDs})
		return err
	})
	if err != nil {
		return User{}, err
	}
	return out, nil
}

type userRoleReplacement struct {
	userID  string
	version int64
	roleIDs []string
}

func (s *AuthService) replaceUserRoles(ctx context.Context, tx *sql.Tx, x Session, command userRoleReplacement) (User, error) {
	userID, valid := canonicalMembershipID(command.userID)
	if !valid {
		return User{}, ErrValidation
	}
	policies, err := boundedRolePolicies(ctx, tx)
	if err != nil {
		return User{}, err
	}
	before, err := membershipAccess(ctx, tx, policies)
	if err != nil {
		return User{}, err
	}
	user, err := findUser(ctx, tx, userID)
	if err != nil {
		return User{}, err
	}
	actor := before[x.user]
	if !withinEnvelope(actor.active, before[user.ID].latent) {
		return User{}, ErrNotFound
	}
	if command.version < 1 {
		return User{}, ErrValidation
	}
	if user.Version != command.version {
		return User{}, ErrVersionConflict
	}
	requested, err := validateUserRoleIDs(command.roleIDs, policies, actor.active)
	if err != nil {
		return User{}, err
	}
	// Enrollment requires a pending account to have no memberships.
	if user.State == userStatePending && len(requested) != 0 {
		return User{}, ErrInvalidTransition
	}
	if err = writeUserRoles(ctx, tx, &user, requested); err != nil {
		return User{}, err
	}
	after, err := membershipAccess(ctx, tx, policies)
	if err != nil {
		return User{}, err
	}
	if !withinEnvelope(actor.active, after[user.ID].latent) || !ownAccessDoesNotGrow(actor, after[x.user]) {
		return User{}, ErrPermissionDenied
	}
	if err = s.requireAccessAdmin(ctx, tx, after); err != nil {
		return User{}, err
	}
	return user, securityAudit(ctx, tx, userRolesReplacedEvent, x.user, user.ID, "")
}

func validateUserRoleIDs(input []string, policies map[string][]accessGrant, actor []accessGrant) ([]string, error) {
	if input == nil || len(input) > MaxUserRoles {
		return nil, ErrValidation
	}
	ids := make([]string, 0, len(input))
	for _, id := range input {
		canonical, valid := canonicalMembershipID(id)
		if !valid || slices.Contains(ids, canonical) {
			return nil, ErrValidation
		}
		ids = append(ids, canonical)
	}
	for _, id := range ids {
		grants, exists := policies[id]
		if !exists || !withinEnvelope(actor, grants) {
			return nil, ErrNotFound
		}
	}
	return ids, nil
}

func canonicalMembershipID(id string) (string, bool) {
	var parsed pgtype.UUID
	if parsed.Scan(id) != nil || !parsed.Valid || parsed.Bytes == [16]byte{} {
		return "", false
	}
	canonical := parsed.String()
	return canonical, strings.EqualFold(id, canonical)
}

func writeUserRoles(ctx context.Context, tx *sql.Tx, user *User, requested []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.user_roles WHERE user_id=$1
 AND role_id NOT IN (SELECT id::uuid FROM unnest($2::text[]) AS requested(id))`, user.ID, requested); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.user_roles(user_id,role_id)
 SELECT $1,id::uuid FROM unnest($2::text[]) AS requested(id) ORDER BY id
 ON CONFLICT (user_id,role_id) DO NOTHING`, user.ID, requested); err != nil {
		return err
	}
	return tx.QueryRowContext(ctx, `UPDATE public.users SET version=version+1 WHERE id=$1 RETURNING version`, user.ID).Scan(&user.Version)
}
