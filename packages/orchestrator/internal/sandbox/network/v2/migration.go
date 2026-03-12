package v2

import "net"

// MigrationDomain describes a migration/snapshot restore domain for a sandbox.
// This is a stub data model for the PoC — behavior will be added later.
type MigrationDomain struct {
	ID string

	// SourceNode is the node the sandbox is migrating from.
	SourceNode string

	// TargetNode is the node the sandbox is migrating to.
	TargetNode string

	// SandboxID is the sandbox being migrated.
	SandboxID string

	// HostIP is the allocated host IP for the sandbox (preserved across migration).
	HostIP net.IP

	// SlotIdx is the slot index on the target node.
	SlotIdx int

	// State tracks migration progress.
	State MigrationState
}

type MigrationState string

const (
	MigrationStatePending   MigrationState = "pending"
	MigrationStateActive    MigrationState = "active"
	MigrationStateCompleted MigrationState = "completed"
	MigrationStateFailed    MigrationState = "failed"
)

// DefaultMigrationDomain returns a no-op domain (no migration in progress).
func DefaultMigrationDomain() *MigrationDomain {
	return &MigrationDomain{
		State: MigrationStateCompleted,
	}
}
