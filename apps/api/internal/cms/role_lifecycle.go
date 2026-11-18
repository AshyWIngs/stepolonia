package cms

import (
	"context"
	"database/sql"
	"slices"
)

const (
	roleStateActive   = "active"
	roleStateDisabled = "disabled"
	roleStateArchived = "archived"
)

const (
	opRolesEnable  = "roles.enable"
	opRolesDisable = "roles.disable"
	opRolesArchive = "roles.archive"
	opRolesRestore = "roles.restore"
)

type roleTransition struct {
	permission, auditEvent string
	from, to               string
}

// EnableRole activates a disabled role for its existing recipients.
func (s *AuthService) EnableRole(ctx context.Context, req AuthRequest, id string, expectedVersion int64) (Role, error) {
	return s.changeRoleState(ctx, req, id, expectedVersion, roleTransition{opRolesEnable, "roles.enabled", roleStateDisabled, roleStateActive})
}

// DisableRole removes a role's effective grants while preserving memberships.
func (s *AuthService) DisableRole(ctx context.Context, req AuthRequest, id string, expectedVersion int64) (Role, error) {
	return s.changeRoleState(ctx, req, id, expectedVersion, roleTransition{opRolesDisable, "roles.disabled", roleStateActive, roleStateDisabled})
}

// ArchiveRole archives a disabled role and retains its explicit policy.
func (s *AuthService) ArchiveRole(ctx context.Context, req AuthRequest, id string, expectedVersion int64) (Role, error) {
	return s.changeRoleState(ctx, req, id, expectedVersion, roleTransition{opRolesArchive, "roles.archived", roleStateDisabled, roleStateArchived})
}

// RestoreRole returns an archived role to the disabled state.
func (s *AuthService) RestoreRole(ctx context.Context, req AuthRequest, id string, expectedVersion int64) (Role, error) {
	return s.changeRoleState(ctx, req, id, expectedVersion, roleTransition{opRolesRestore, "roles.restored", roleStateArchived, roleStateDisabled})
}

func (s *AuthService) changeRoleState(ctx context.Context, req AuthRequest, id string, version int64, transition roleTransition) (Role, error) {
	var out Role
	err := s.authorizedCommand(ctx, req, transition.permission, transition.auditEvent, func(ctx context.Context, tx *sql.Tx, x Session) error {
		plan, err := prepareRoleMutation(ctx, tx, x, id, version)
		if err != nil {
			return err
		}
		out, err = s.applyRoleTransition(ctx, tx, x, plan, transition)
		return err
	})
	if err != nil {
		return Role{}, err
	}
	return out, nil
}

func (s *AuthService) applyRoleTransition(ctx context.Context, tx *sql.Tx, x Session, plan roleMutation, transition roleTransition) (Role, error) {
	out := plan.role
	if out.State != transition.from {
		return Role{}, ErrInvalidTransition
	}
	// Only activation can grow effective access. The role's explicit policy,
	// memberships and every recipient's latent grants remain unchanged.
	if transition.to == roleStateActive && slices.Contains(plan.recipients, x.user) && !accessDoesNotGrow(plan.before[x.user].active, plan.policies[out.ID]) {
		return Role{}, ErrPermissionDenied
	}
	err := tx.QueryRowContext(ctx, `UPDATE public.roles SET state=$2,version=version+1 WHERE id=$1 RETURNING state,version`, out.ID, transition.to).Scan(&out.State, &out.Version)
	if err != nil {
		return Role{}, err
	}
	after := plan.before
	if transition.from == roleStateActive || transition.to == roleStateActive {
		after, err = membershipAccess(ctx, tx, plan.policies)
		if err != nil {
			return Role{}, err
		}
	}
	if err = s.requireAccessAdmin(ctx, tx, after); err != nil {
		return Role{}, err
	}
	return out, securityAudit(ctx, tx, transition.auditEvent, x.user, out.ID, "")
}
