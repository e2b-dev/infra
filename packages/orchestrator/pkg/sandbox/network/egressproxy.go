package network

import (
	"github.com/coreos/go-iptables/iptables"
)

type CACertificate struct {
	// Name is the filename (without extension) used when installing the cert into the trust store.
	Name string

	// Cert is the PEM-encoded CA certificate.
	Cert string
}

type EgressProxy interface {
	OnSlotCreate(s *Slot, tables *iptables.IPTables) error
	OnSlotDelete(s *Slot, tables *iptables.IPTables) error

	CACertificates() []CACertificate
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

func (NoopEgressProxy) CACertificates() []CACertificate {
	return nil
}
