//go:build linux

package network

// EgressProxy is notified of per-sandbox lifecycle so it can perform any
// extra netns setup the slot creation alone can't express (e.g. removing
// the default route from a no-egress sandbox).
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
