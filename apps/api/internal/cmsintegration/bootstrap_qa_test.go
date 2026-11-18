package cmsintegration

import (
	"context"
	"stepolonia/apps/api/internal/cms"
	"testing"
)

func TestBootstrapQASameLoginConcurrentRestartIntegration(t *testing.T) {
	db, svc, conf := bootstrapFixture(t)
	ctx := context.Background()
	_, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
	requireNoError(t, err)
	other, err := cms.NewBootstrapService(db, conf)
	requireNoError(t, err)
	lock := holdInstallationWriteMutex(t, db)
	out := make(chan error, 2)
	for _, caller := range []*cms.BootstrapService{svc, other} {
		go func() {
			_, err := caller.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
			out <- err
		}()
	}
	waitForInstallationWriteWaiters(t, db, 2)
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
	lock := holdInstallationWriteMutex(t, db)
	restarted := make(chan error, 1)
	completed := make(chan error, 1)
	go func() {
		_, err := svc.Start(ctx, bootstrapTestLogin, bootstrapTestPassphrase)
		restarted <- err
	}()
	waitForInstallationWriteWaiters(t, db, 1)
	go func() {
		_, err := svc.Complete(ctx, bootstrapTestLogin, bootstrapTestPassphrase, code)
		completed <- err
	}()
	waitForInstallationWriteWaiters(t, db, 2)
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
