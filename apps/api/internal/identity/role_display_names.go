package identity

import (
	"context"
	"database/sql"
)

func roleDisplayNames(ctx context.Context, tx *sql.Tx, ids []string) (map[string]map[string]string, error) {
	result := make(map[string]map[string]string)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.role_id::text,d.language_code,d.display_name
 FROM public.role_display_names d JOIN public.public_languages l ON l.code=d.language_code
 WHERE d.role_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var roleID, locale, name string
		if err := rows.Scan(&roleID, &locale, &name); err != nil {
			return nil, err
		}
		if result[roleID] == nil {
			result[roleID] = make(map[string]string)
		}
		result[roleID][locale] = name
	}
	return result, rows.Err()
}

// SetRoleDisplayName changes one localized label under the standard edit boundary.
func (s *AuthService) SetRoleDisplayName(ctx context.Context, req AuthRequest, id string, version int64, locale string, name *string) (Role, error) {
	var out Role
	err := s.authorizedCommand(ctx, req, []string{opRolesEdit}, roleEditedEvent, func(ctx context.Context, tx *sql.Tx, actor Session) error {
		plan, err := prepareRoleMutation(ctx, tx, actor, id, version)
		if err != nil {
			return err
		}
		if plan.role.bound {
			return ErrPermissionDenied
		}
		if err := writeRoleDisplayName(ctx, tx, id, locale, name); err != nil {
			return err
		}
		out = plan.role
		if err := tx.QueryRowContext(ctx, `UPDATE public.roles SET version=version+1 WHERE id=$1 RETURNING version`, id).Scan(&out.Version); err != nil {
			return err
		}
		if out.DisplayNames == nil {
			out.DisplayNames = make(map[string]string)
		}
		if name == nil {
			delete(out.DisplayNames, locale)
		} else {
			out.DisplayNames[locale] = *name
		}
		return securityAudit(ctx, tx, roleEditedEvent, actor.user, id, "")
	})
	if err != nil {
		return Role{}, err
	}
	return out, nil
}

func writeRoleDisplayName(ctx context.Context, tx *sql.Tx, roleID, locale string, name *string) error {
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.public_languages WHERE code=$1)`, locale).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return ErrValidation
	}
	if name == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM public.role_display_names WHERE role_id=$1 AND language_code=$2`, roleID, locale)
		return err
	}
	if _, err := normalizeRoleName(*name); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO public.role_display_names (role_id,language_code,display_name)
 VALUES ($1,$2,$3) ON CONFLICT (role_id,language_code) DO UPDATE SET display_name=EXCLUDED.display_name`,
		roleID, locale, *name)
	return err
}
