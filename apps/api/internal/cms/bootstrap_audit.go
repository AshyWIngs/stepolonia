package cms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Denials are recorded only for an existing pending enrollment. The business
// transaction has rolled back before this independent bounded transaction runs.
func (s *BootstrapService) auditBootstrapDenial(ctx context.Context, snap bootstrapSnapshot, denial error) error {
	if !errors.Is(denial, errBootstrapDenied) || snap.id == "" {
		return denial
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := WithWriteMutex(auditCtx, s.db, func(ctx context.Context, tx *sql.Tx) error {
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.security_state s JOIN public.users u ON u.id=s.bootstrap_user_id WHERE s.id=1 AND NOT s.bootstrap_completed AND s.site=$1 AND u.id=$2 AND u.state='pending') AND current_user=session_user AND current_user=(SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_catalog.pg_database WHERE datname=current_database())`, s.config.Site, snap.id).Scan(&pending); err != nil {
			return err
		}
		if !pending {
			return nil
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public.security_audit(id,operation,result,target_id,reason) VALUES($1,'bootstrap.denied','denied',$2,'invalid_credentials')`, id, snap.id)
		return err
	})
	if err != nil {
		return errors.Join(denial, fmt.Errorf("audit bootstrap denial: %w", err))
	}
	return denial
}
