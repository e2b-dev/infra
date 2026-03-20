package egressproxy

import (
	"github.com/coreos/go-iptables/iptables"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

var _ sandbox.MapSubscriber = (EgressProxy)(nil)

type EgressProxy interface {
	// Handlers used for registering sandbox creation/removal lifecycle.
	OnInsert(sandbox *sandbox.Sandbox)
	OnRemove(sandboxID string)

	// Handlers used for registering network slot creation/removal lifecycle.
	OnSlotCreate(s *network.Slot, tables *iptables.IPTables) error
	OnSlotDelete(s *network.Slot, tables *iptables.IPTables) error
}
