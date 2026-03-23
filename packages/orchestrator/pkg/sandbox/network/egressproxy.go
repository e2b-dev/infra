package network

import (
	"github.com/coreos/go-iptables/iptables"
)

type EgressProxy interface {
	OnSlotCreate(s *Slot, tables *iptables.IPTables) error
	OnSlotDelete(s *Slot, tables *iptables.IPTables) error
}

// NoopEgressProxy is a no-op implementation of EgressProxy.
type NoopEgressProxy struct{}

func (NoopEgressProxy) OnSlotCreate(_ *Slot, _ *iptables.IPTables) error { return nil }
func (NoopEgressProxy) OnSlotDelete(_ *Slot, _ *iptables.IPTables) error { return nil }
