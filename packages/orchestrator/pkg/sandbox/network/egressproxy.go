package network

import (
	"github.com/coreos/go-iptables/iptables"
)

type EgressProxy interface {
	OnSlotCreate(s *Slot, tables *iptables.IPTables) error
	OnSlotDelete(s *Slot, tables *iptables.IPTables) error

	CABundle() string
}

// NoopEgressProxy is a no-op implementation of EgressProxy.
type NoopEgressProxy struct{}

var _ EgressProxy = (*NoopEgressProxy)(nil)

func NewNoopEgressProxy() NoopEgressProxy {
	return NoopEgressProxy{}
}

func (NoopEgressProxy) OnSlotCreate(_ *Slot, _ *iptables.IPTables) error {
	return nil
}

func (NoopEgressProxy) OnSlotDelete(_ *Slot, _ *iptables.IPTables) error {
	return nil
}

func (NoopEgressProxy) CABundle() string {
	return ""
}
