package v2

import (
	"fmt"
	"sync"
)

// VethObserver provides per-veth packet/byte counters via tc/eBPF.
// It is nil-safe: if BPF is unavailable, NewVethObserver returns (nil, nil)
// and all methods on a nil *VethObserver are no-ops.
//
// For the PoC, this is a placeholder that tracks attachment state.
// Full eBPF counter implementation requires cilium/ebpf + BPF program.
type VethObserver struct {
	mu       sync.Mutex
	attached map[string]bool // veth name → attached
}

// NewVethObserver creates a new observer. Returns (nil, nil) if eBPF is not available.
func NewVethObserver() (*VethObserver, error) {
	// TODO: Check BPF availability and load compiled BPF program.
	// For now, always succeed with a no-op observer that tracks state.
	return &VethObserver{
		attached: make(map[string]bool),
	}, nil
}

// Attach attaches the eBPF counter program to a veth interface.
func (o *VethObserver) Attach(vethName string) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.attached[vethName] {
		return fmt.Errorf("already attached to %s", vethName)
	}
	// TODO: tc qdisc add + BPF filter attach
	o.attached[vethName] = true
	return nil
}

// Detach removes the eBPF counter from a veth interface.
func (o *VethObserver) Detach(vethName string) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.attached[vethName] {
		return nil // already detached, idempotent
	}
	// TODO: tc qdisc del
	delete(o.attached, vethName)
	return nil
}

// ReadCounters reads the packet and byte counters for a veth interface.
func (o *VethObserver) ReadCounters(vethName string) (packets, bytes uint64, err error) {
	if o == nil {
		return 0, 0, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.attached[vethName] {
		return 0, 0, fmt.Errorf("not attached to %s", vethName)
	}
	// TODO: Read from BPF map
	return 0, 0, nil
}

// Close detaches all observers.
func (o *VethObserver) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	for name := range o.attached {
		// TODO: actual tc cleanup
		delete(o.attached, name)
	}
	return nil
}
