//go:build linux

package network

// EgressProxy participates in per-sandbox host-side iptables setup by
// appending its own PREROUTING rules into the shared RuleSet that is flushed
// once per CreateNetwork / RemoveNetwork.
type EgressProxy interface {
	OnSlotCreate(s *Slot, rules *RuleSet) error
	OnSlotDelete(s *Slot, rules *RuleSet) error

	CABundle() string
}

// NoopEgressProxy is a no-op implementation of EgressProxy.
type NoopEgressProxy struct{}

var _ EgressProxy = (*NoopEgressProxy)(nil)

func NewNoopEgressProxy() NoopEgressProxy {
	return NoopEgressProxy{}
}

func (NoopEgressProxy) OnSlotCreate(_ *Slot, _ *RuleSet) error {
	return nil
}

func (NoopEgressProxy) OnSlotDelete(_ *Slot, _ *RuleSet) error {
	return nil
}

func (NoopEgressProxy) CABundle() string {
	return ""
}
