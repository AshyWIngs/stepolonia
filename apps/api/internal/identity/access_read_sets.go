package identity

import (
	"context"
	"database/sql"
	"slices"
)

// RoleMembershipSet provides the current full replacement set and visible role choices.
type RoleMembershipSet struct {
	UserID  string
	Version int64
	RoleIDs []string
	Options []RoleMembershipOption
}

// RoleMembershipOption contains only the identity, display name and lifecycle state.
type RoleMembershipOption struct {
	ID, Name, State string
}

// ReadUserRoleMemberships checks the same effective assignment authority and
// latent target envelope as the write, while leaving step-up to the mutation.
func (s *AuthService) ReadUserRoleMemberships(ctx context.Context, req AuthRequest, id string) (RoleMembershipSet, error) {
	var out RoleMembershipSet
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requirePermission(ctx, tx, session.user, opRolesAssign); err != nil {
			return err
		}
		userID, valid := canonicalMembershipID(id)
		if !valid {
			return ErrValidation
		}
		policies, err := boundedRolePolicies(ctx, tx)
		if err != nil {
			return err
		}
		access, err := membershipAccess(ctx, tx, policies)
		if err != nil {
			return err
		}
		user, err := visibleRoleAssignmentTarget(ctx, tx, userID, access, access[session.user].active)
		if err != nil {
			return err
		}
		out, err = readRoleMembershipOptions(ctx, tx, user, session.user, policies, access[session.user])
		return err
	})
	if err != nil {
		return RoleMembershipSet{}, err
	}
	return out, nil
}

func readRoleMembershipOptions(ctx context.Context, tx *sql.Tx, user User, actorID string, policies map[string][]accessGrant, actor userAccess) (RoleMembershipSet, error) {
	out := RoleMembershipSet{UserID: user.ID, Version: user.Version, RoleIDs: []string{}, Options: []RoleMembershipOption{}}
	rows, err := tx.QueryContext(ctx, `SELECT r.id::text, r.name, r.state,
 r.self_access_user_id IS NOT NULL, m.role_id IS NOT NULL
 FROM public.roles r LEFT JOIN public.user_roles m ON m.role_id=r.id AND m.user_id=$1
 ORDER BY r.name,r.id`, user.ID)
	if err != nil {
		return RoleMembershipSet{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var option RoleMembershipOption
		var bound, existing bool
		if err := rows.Scan(&option.ID, &option.Name, &option.State, &bound, &existing); err != nil {
			return RoleMembershipSet{}, err
		}
		if existing {
			out.RoleIDs = append(out.RoleIDs, option.ID)
		} else if bound || user.State == userStatePending || !roleMembershipChoice(policies[option.ID], option.State, user.ID == actorID, actor) {
			continue
		}
		out.Options = append(out.Options, option)
	}
	slices.Sort(out.RoleIDs)
	return out, rows.Err()
}

func roleMembershipChoice(policy []accessGrant, state string, own bool, actor userAccess) bool {
	if !withinEnvelope(actor.active, policy) {
		return false
	}
	if !own {
		return true
	}
	return accessDoesNotGrow(actor.latent, policy) &&
		(state != roleStateActive || accessDoesNotGrow(actor.active, policy))
}

// ContentAssignmentSet provides the current full replacement set and visible account choices.
type ContentAssignmentSet struct {
	ContentAssignees
	Options []ContentAssigneeOption
}

// ContentAssigneeOption identifies a possible assignee or an existing inactive
// assignee. Inactive choices may only be removed.
type ContentAssigneeOption struct {
	ID, Login, State string
}

// ReadContentAssignees returns the complete assignment set only when the actor
// can manage the item and every current recipient is within the envelope.
func (s *AuthService) ReadContentAssignees(ctx context.Context, req AuthRequest, typeKey, itemID string) (ContentAssignmentSet, error) {
	var out ContentAssignmentSet
	err := s.WithSession(ctx, req, false, func(ctx context.Context, tx *sql.Tx, session Session) error {
		if err := requireContentAssignment(ctx, tx, session.user); err != nil {
			return err
		}
		item, err := findAssignmentItem(ctx, tx, session.user, typeKey, itemID)
		if err != nil {
			return err
		}
		access, err := loadAccess(ctx, tx)
		if err != nil {
			return err
		}
		out, err = readContentAssigneeOptions(ctx, tx, item, session.user, access)
		return err
	})
	if err != nil {
		return ContentAssignmentSet{}, err
	}
	return out, nil
}

func readContentAssigneeOptions(ctx context.Context, tx *sql.Tx, item assigneeItem, actorID string, access accessSnapshot) (ContentAssignmentSet, error) {
	out := ContentAssignmentSet{
		ContentAssignees: ContentAssignees{ItemID: item.id, Version: item.version, AssigneeIDs: []string{}},
		Options:          []ContentAssigneeOption{},
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.id::text, u.login_key, u.state, a.user_id IS NOT NULL
 FROM public.users u LEFT JOIN public.content_item_assignments a ON a.user_id=u.id AND a.item_id=$1
 WHERE u.state='active' OR a.user_id IS NOT NULL ORDER BY u.login_key,u.id`, item.id)
	if err != nil {
		return ContentAssignmentSet{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var option ContentAssigneeOption
		var existing bool
		if err := rows.Scan(&option.ID, &option.Login, &option.State, &existing); err != nil {
			return ContentAssignmentSet{}, err
		}
		if !existing && option.ID == actorID {
			continue
		}
		if option.ID != actorID && !withinAssigneeEnvelope(access[actorID].active, access[option.ID].latent, item.typeID) {
			if existing {
				return ContentAssignmentSet{}, ErrNotFound
			}
			continue
		}
		if existing {
			out.AssigneeIDs = append(out.AssigneeIDs, option.ID)
		}
		out.Options = append(out.Options, option)
	}
	slices.Sort(out.AssigneeIDs)
	return out, rows.Err()
}
