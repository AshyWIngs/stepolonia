package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

// Hold the installation mutex until both callers have completed password work.
// Observing PostgreSQL lock waiters makes the stale snapshot race deterministic.
func qaBootstrapLock(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	requireNoError(t, err)
	_, err = tx.Exec(cms.WriteMutexSQL)
	requireNoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func qaBootstrapWaiters(t *testing.T, db *sql.DB, wanted int) {
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
			t.Fatal("bootstrap callers did not reach installation mutex")
		case <-ticker.C:
		}
	}
}

func TestBootstrapQASameLoginConcurrentRestartIntegration(t *testing.T) {
	db, svc, conf := bootstrapFixture(t)
	ctx := context.Background()
	_, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	other, err := cms.NewBootstrapService(db, conf)
	requireNoError(t, err)
	lock := qaBootstrapLock(t, db)
	out := make(chan error, 2)
	for _, caller := range []*cms.BootstrapService{svc, other} {
		go func() {
			_, err := caller.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
			out <- err
		}()
	}
	qaBootstrapWaiters(t, db, 2)
	requireNoError(t, lock.Commit())
	successes := 0
	for range 2 {
		if <-out == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent same-login restarts committed %d times", successes)
	}
	assertBootstrapPending(t, db)
	var restarts int
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM public.security_audit WHERE operation='bootstrap.restarted'`).Scan(&restarts))
	if restarts != 1 {
		t.Fatal("concurrent restart committed duplicate audit or seed")
	}
}

func TestBootstrapQAStaleCompleteAfterRestartIntegration(t *testing.T) {
	db, svc, _ := bootstrapFixture(t)
	ctx := context.Background()
	old, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	code := bootstrapCode(t, db, old.Secret)
	lock := qaBootstrapLock(t, db)
	restarted := make(chan error, 1)
	completed := make(chan error, 1)
	go func() {
		_, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
		restarted <- err
	}()
	qaBootstrapWaiters(t, db, 1)
	go func() {
		_, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code)
		completed <- err
	}()
	qaBootstrapWaiters(t, db, 2)
	requireNoError(t, lock.Commit())
	requireNoError(t, <-restarted)
	if <-completed == nil {
		t.Fatal("completion authenticated before restart activated stale seed")
	}
	assertBootstrapPending(t, db)
	var completions int
	requireNoError(t, db.QueryRow(`SELECT count(*) FROM public.security_audit WHERE operation='bootstrap.completed'`).Scan(&completions))
	if completions != 0 {
		t.Fatal("stale completion committed success audit")
	}
}
