package egressproxy

import (
	"github.com/coreos/go-iptables/iptables"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

// Packages using orchestrator as library cannot import from internal package, this is golang limitation,
// this is why alias are used here to export types from internal package.
type (
	SandboxInfo        = sandbox.Sandbox
	SandboxNetworkSlot = network.Slot
)

type EgressProxy interface {
	// Handlers used for registering sandbox creation/removal lifecycle.
	OnInsert(sandbox *SandboxInfo)
	OnRemove(sandboxID string)

	// Handlers used for registering network slot creation/removal lifecycle.
	OnNetworkSlotInsert(s *SandboxNetworkSlot, tables *iptables.IPTables) error
	OnNetworkSlotDelete(s *SandboxNetworkSlot, tables *iptables.IPTables) []error
}
