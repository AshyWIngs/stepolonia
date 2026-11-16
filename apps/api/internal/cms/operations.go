// Package cms contains dormant CMS foundations; it does not expose authentication or writers.
package cms

import "fmt"

// Operation is a registered action, never a role name or a client-defined capability.
type Operation string

// OperationScope distinguishes exact content grants from installation operations.
type OperationScope uint8

const (
	// InstallationScope does not itself grant access to content data.
	InstallationScope OperationScope = iota + 1
	// ContentScope requires explicit type, item scope and field authorization.
	ContentScope
)

// Scope returns the operation's allowed grant category. Unknown operations fail closed.
func (o Operation) Scope() (OperationScope, error) {
	switch o {
	case "content.items.create", "content.items.read", "content.items.update",
		"content.items.ready", "content.items.publish", "content.items.unpublish", "content.items.assign":
		return ContentScope, nil
	case "content_types.manage", "content_types.initialize_access", "content.permissions.delegate",
		"users.create", "users.read", "users.activate", "users.disable",
		"roles.create", "roles.read", "roles.edit", "roles.assign", "roles.disable", "roles.enable",
		"roles.archive", "roles.restore", "roles.delete", "sessions.revoke_any", "audit.read", "settings.manage":
		return InstallationScope, nil
	default:
		return 0, fmt.Errorf("unregistered CMS operation %q", o)
	}
}
