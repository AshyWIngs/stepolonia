// Package cms contains guarded CMS foundations and operator-only bootstrap enrollment.
package cms

import "fmt"

// Operation is a registered action, never a role name or a client-defined capability.
type Operation string

// Operations shared with content writers are owned by the authorization registry.
const (
	OpContentRead        Operation = "content.items.read"
	OpContentUpdate      Operation = "content.items.update"
	OpContentTypesManage Operation = "content_types.manage"
)

const (
	opContentCreate           = "content.items.create"
	opContentReady            = "content.items.ready"
	opContentPublish          = "content.items.publish"
	opContentDelegate         = "content.permissions.delegate"
	opContentInitializeAccess = "content_types.initialize_access"
	opSettingsManage          = "settings.manage"
)

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
	case opContentCreate, OpContentRead, OpContentUpdate,
		opContentReady, opContentPublish, "content.items.unpublish", "content.items.assign":
		return ContentScope, nil
	case OpContentTypesManage, opContentInitializeAccess, opContentDelegate,
		"users.create", "users.read", "users.activate", "users.disable",
		"roles.create", "roles.read", "roles.edit", "roles.assign", "roles.disable", "roles.enable",
		"roles.archive", "roles.restore", "roles.delete", "sessions.revoke_any", "audit.read", opSettingsManage:
		return InstallationScope, nil
	default:
		return 0, fmt.Errorf("unregistered CMS operation %q", o)
	}
}
