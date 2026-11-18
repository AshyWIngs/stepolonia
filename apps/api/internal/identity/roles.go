package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Role pagination shares the installation's bounded role capacity.
const (
	DefaultRolePageLimit = 20
	MaxRolePageLimit     = maxRoles
	MaxRolePageOffset    = maxRoles
)

const (
	opRolesRead   = "roles.read"
	opRolesCreate = "roles.create"
	opRolesEdit   = "roles.edit"
	opRolesDelete = "roles.delete"
)

// Role exposes policy metadata without recipients, credentials or content values.
type Role struct {
	ID, Name, State string
	Version         int64
	Grants          []RoleGrant
}

// RoleGrant is one explicit grant; no field or type wildcard is supported.
type RoleGrant struct {
	Operation, Scope, TypeID, ObjectScope string
	CanDelegate                           bool
	TargetScope, ObjectCeiling            string
	MayDelegateContent                    bool
	DelegateOperations, FieldIDs          []string
}

// RoleInput replaces the complete explicit name and grant set.
type RoleInput struct {
	Name   string
	Grants []RoleGrant
}

// RolePage selects a stable page ordered by normalized name and identity.
type RolePage struct{ Limit, Offset int }

// RoleList contains bounded role policy metadata.
type RoleList struct {
	Items      []Role
	NextOffset *int
}

func (p RolePage) normalize() (RolePage, error) {
	if p.Limit == 0 {
		p.Limit = DefaultRolePageLimit
	}
	if p.Limit < 1 || p.Limit > MaxRolePageLimit || p.Offset < 0 || p.Offset > MaxRolePageOffset {
		return p, ErrValidation
	}
	return p, nil
}

// ListRoles reads policy metadata after fresh session and installation checks.
func (s *AuthService) ListRoles(ctx context.Context, req AuthRequest, page RolePage) (RoleList, error) {
	out := RoleList{Items: []Role{}}
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if err := requirePermission(ctx, tx, x.user, opRolesRead); err != nil {
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
		out, err = queryRolePage(ctx, tx, page)
		return err
	})
	if err != nil {
		return RoleList{}, err
	}
	return out, nil
}

func queryRolePage(ctx context.Context, tx *sql.Tx, page RolePage) (RoleList, error) {
	out := RoleList{Items: []Role{}}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,name,state,version FROM public.roles ORDER BY name,id LIMIT $1 OFFSET $2`, page.Limit+1, page.Offset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var role Role
		if err = rows.Scan(&role.ID, &role.Name, &role.State, &role.Version); err != nil {
			_ = rows.Close()
			return out, err
		}
		if len(out.Items) == page.Limit {
			next := page.Offset + page.Limit
			out.NextOffset = &next
			break
		}
		out.Items = append(out.Items, role)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	for i := range out.Items {
		out.Items[i].Grants, err = queryRoleGrants(ctx, tx, out.Items[i].ID)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// GetRole returns explicit policy metadata without revealing role recipients.
func (s *AuthService) GetRole(ctx context.Context, req AuthRequest, id string) (Role, error) {
	var out Role
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, x Session) error {
		if err := requirePermission(ctx, tx, x.user, opRolesRead); err != nil {
			return err
		}
		if err := checkAccessCaps(ctx, tx); err != nil {
			return err
		}
		var err error
		out, err = findRole(ctx, tx, id)
		return err
	})
	if err != nil {
		return Role{}, err
	}
	return out, nil
}

func findRole(ctx context.Context, tx *sql.Tx, id string) (Role, error) {
	var out Role
	if !validGrantID(id) {
		return out, ErrValidation
	}
	err := tx.QueryRowContext(ctx, `SELECT id::text,name,state,version FROM public.roles WHERE id=$1`, id).Scan(&out.ID, &out.Name, &out.State, &out.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Grants, err = queryRoleGrants(ctx, tx, id)
	return out, err
}

func validGrantID(id string) bool {
	var parsed pgtype.UUID
	return parsed.Scan(id) == nil && parsed.Valid && len(id) == 36
}

func normalizeRoleName(name string) (string, error) {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 || strings.TrimSpace(name) != name {
		return "", ErrValidation
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrValidation
		}
	}
	return name, nil
}

// CreateRole creates a disabled role without memberships or effective access.
func (s *AuthService) CreateRole(ctx context.Context, req AuthRequest, input RoleInput) (Role, error) {
	var out Role
	const auditEvent = "roles.created"
	err := s.authorizedCommand(ctx, req, []string{opRolesCreate}, auditEvent, func(ctx context.Context, tx *sql.Tx, x Session) error {
		before, err := loadAccess(ctx, tx)
		if err != nil {
			return err
		}
		out, err = s.createRole(ctx, tx, x, normalizeRoleInput(input), before, auditEvent)
		return err
	})
	if err != nil {
		return Role{}, err
	}
	return out, nil
}

func (s *AuthService) createRole(ctx context.Context, tx *sql.Tx, x Session, input RoleInput, before accessSnapshot, auditEvent string) (Role, error) {
	grants, err := validateRoleInput(ctx, tx, input)
	if err != nil {
		return Role{}, err
	}
	if !withinEnvelope(before[x.user].active, grants) {
		return Role{}, ErrPermissionDenied
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM public.roles`).Scan(&count); err != nil {
		return Role{}, err
	}
	if count >= maxRoles {
		return Role{}, ErrInvalidTransition
	}
	var out Role
	out.ID, err = randomID()
	if err != nil {
		return Role{}, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO public.roles(id,name) VALUES($1,$2) RETURNING name,state,version`, out.ID, input.Name).Scan(&out.Name, &out.State, &out.Version)
	if err != nil {
		return Role{}, roleWriteError(err)
	}
	if err = replaceRoleGrants(ctx, tx, out.ID, input.Grants); err != nil {
		return Role{}, err
	}
	if err = s.requireAccessAdmin(ctx, tx, before); err != nil {
		return Role{}, err
	}
	out.Grants, err = queryRoleGrants(ctx, tx, out.ID)
	if err != nil {
		return Role{}, err
	}
	return out, securityAudit(ctx, tx, auditEvent, x.user, out.ID, "")
}

// EditRole atomically replaces explicit policy without state or membership changes.
func (s *AuthService) EditRole(ctx context.Context, req AuthRequest, id string, expectedVersion int64, input RoleInput) (Role, error) {
	var out Role
	err := s.authorizedCommand(ctx, req, []string{opRolesEdit}, "roles.edited", func(ctx context.Context, tx *sql.Tx, x Session) error {
		plan, err := prepareRoleMutation(ctx, tx, x, id, expectedVersion)
		if err != nil {
			return err
		}
		out, err = s.applyRoleEdit(ctx, tx, x, plan, normalizeRoleInput(input))
		return err
	})
	if err != nil {
		return Role{}, err
	}
	return out, nil
}

type roleMutation struct {
	role       Role
	policies   map[string][]accessGrant
	before     accessSnapshot
	recipients []string
}

func prepareRoleMutation(ctx context.Context, tx *sql.Tx, x Session, id string, version int64) (roleMutation, error) {
	var plan roleMutation
	var err error
	plan.policies, err = boundedRolePolicies(ctx, tx)
	if err != nil {
		return plan, err
	}
	plan.before, err = membershipAccess(ctx, tx, plan.policies)
	if err != nil {
		return plan, err
	}
	plan.role, err = findRole(ctx, tx, id)
	if err != nil {
		return plan, err
	}
	actor := plan.before[x.user].active
	if !withinEnvelope(actor, plan.policies[plan.role.ID]) {
		return plan, ErrNotFound
	}
	plan.recipients, err = roleRecipients(ctx, tx, id)
	if err != nil {
		return plan, err
	}
	if !recipientsWithinEnvelope(actor, plan.before, plan.recipients) {
		return plan, ErrNotFound
	}
	if version < 1 {
		return plan, ErrValidation
	}
	if plan.role.Version != version {
		return plan, ErrVersionConflict
	}
	return plan, nil
}

func (s *AuthService) applyRoleEdit(ctx context.Context, tx *sql.Tx, x Session, plan roleMutation, input RoleInput) (Role, error) {
	grants, err := validateRoleInput(ctx, tx, input)
	if err != nil {
		return Role{}, err
	}
	if !withinEnvelope(plan.before[x.user].active, grants) {
		return Role{}, ErrPermissionDenied
	}
	out := plan.role
	err = tx.QueryRowContext(ctx, `UPDATE public.roles SET name=$2,version=version+1 WHERE id=$1 RETURNING name,version`, out.ID, input.Name).Scan(&out.Name, &out.Version)
	if err != nil {
		return Role{}, roleWriteError(err)
	}
	if err = replaceRoleGrants(ctx, tx, out.ID, input.Grants); err != nil {
		return Role{}, err
	}
	after, err := loadAccess(ctx, tx)
	if err != nil {
		return Role{}, err
	}
	if !recipientsWithinEnvelope(plan.before[x.user].active, after, plan.recipients) || !ownAccessDoesNotGrow(plan.before[x.user], after[x.user]) {
		return Role{}, ErrPermissionDenied
	}
	if err = s.requireAccessAdmin(ctx, tx, after); err != nil {
		return Role{}, err
	}
	out.Grants, err = queryRoleGrants(ctx, tx, out.ID)
	if err != nil {
		return Role{}, err
	}
	return out, securityAudit(ctx, tx, "roles.edited", x.user, out.ID, "")
}

func ownAccessDoesNotGrow(before, after userAccess) bool {
	return accessDoesNotGrow(before.active, after.active) && accessDoesNotGrow(before.latent, after.latent)
}

func roleWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrVersionConflict
	}
	return err
}
