//go:build linux

package network

// EgressProxy is notified of per-sandbox lifecycle so it can perform any
// extra setup the slot creation alone can't express (e.g. removing the
// default route from a no-egress sandbox). It used to inject per-sandbox
// iptables rules; the host-side ruleset now lives in HostFirewall and is
// driven by veth-set membership, so the callbacks no longer take an
// iptables / RuleSet handle.
type EgressProxy interface {
	OnSlotCreate(s *Slot) error
	OnSlotDelete(s *Slot) error

	CABundle() string
}

// NoopEgressProxy is a no-op implementation of EgressProxy.
type NoopEgressProxy struct{}

var _ EgressProxy = (*NoopEgressProxy)(nil)

func NewNoopEgressProxy() NoopEgressProxy {
	return NoopEgressProxy{}
}

func (NoopEgressProxy) OnSlotCreate(_ *Slot) error { return nil }
func (NoopEgressProxy) OnSlotDelete(_ *Slot) error { return nil }
func (NoopEgressProxy) CABundle() string           { return "" }
