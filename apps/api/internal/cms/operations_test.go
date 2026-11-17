package cms

import (
	"context"
	"database/sql"
	"testing"
)

func TestOperationScopes(t *testing.T) {
	for _, op := range []Operation{"content.items.create", "content.items.read", "content.items.update", "content.items.ready", "content.items.publish", "content.items.unpublish", "content.items.assign"} {
		if got, err := op.Scope(); err != nil || got != ContentScope {
			t.Fatalf("%s: scope=%d err=%v", op, got, err)
		}
	}
	for _, op := range []Operation{"content_types.manage", "content_types.initialize_access", "content.permissions.delegate", "users.create", "users.read", "users.activate", "users.disable", "roles.create", "roles.read", "roles.edit", "roles.assign", "roles.disable", "roles.enable", "roles.archive", "roles.restore", "roles.delete", "sessions.revoke_any", "audit.read", "settings.manage"} {
		if got, err := op.Scope(); err != nil || got != InstallationScope {
			t.Fatalf("%s: scope=%d err=%v", op, got, err)
		}
	}
	for _, op := range []Operation{"", "*", "Site Admin", "content.items.*", "users.delete", "content.items.read ", "CONTENT.ITEMS.READ"} {
		if got, err := op.Scope(); err == nil || got != 0 {
			t.Fatalf("unregistered operation %q allowed: %d %v", op, got, err)
		}
	}
}

func TestTransactionRejectsMissingDependencies(t *testing.T) {
	called := false
	err := WithWriteMutex(context.Background(), nil, func(context.Context, *sql.Tx) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatal("missing database must fail before callback")
	}
	if err := WithWriteMutex(context.Background(), &sql.DB{}, nil); err == nil {
		t.Fatal("missing command must fail before transaction")
	}
}
