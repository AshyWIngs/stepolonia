package identity

import (
	"context"
	"database/sql"
	"fmt"
)

// WriteMutexSQL is shared with statement guards in the installation database.
// The fixed pair identifies the installation write lock; locks are database-local.
const WriteMutexSQL = "SELECT pg_catalog.pg_advisory_xact_lock(1398031696, 1)"

// WithWriteMutex runs one bounded READ COMMITTED transaction under the installation write lock.
// It supplies serialization only: callers must re-read and authorize current state
// after acquiring the lock. The callback must not commit, rollback or retain tx.
func WithWriteMutex(ctx context.Context, db *sql.DB, command func(context.Context, *sql.Tx) error) error {
	if db == nil || command == nil {
		return fmt.Errorf("installation transaction requires database and command")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin installation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '15s'"); err != nil {
		return fmt.Errorf("bound installation transaction: %w", err)
	}
	// A separate statement gives subsequent reads a fresh snapshot after a wait.
	if _, err := tx.ExecContext(ctx, WriteMutexSQL); err != nil {
		return fmt.Errorf("lock installation transaction: %w", err)
	}
	if err := command(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit installation transaction: %w", err)
	}
	return nil
}
