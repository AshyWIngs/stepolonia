package cmsintegration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

// Hold the common write mutex to control authorization and revocation order.
func holdInstallationWriteMutex(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	requireNoError(t, err)
	_, err = tx.Exec(cms.WriteMutexSQL)
	requireNoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func waitForInstallationWriteWaiters(t *testing.T, db *sql.DB, wanted int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var n int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_catalog.pg_locks
WHERE locktype='advisory' AND NOT granted
AND database=(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database())`).Scan(&n)
		requireNoError(t, err)
		if n == wanted {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("protected requests did not reach the installation mutex")
		case <-ticker.C:
		}
	}
}
