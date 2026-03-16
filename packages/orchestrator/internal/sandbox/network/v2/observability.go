package v2

import (
	"fmt"
	"sync"

	"github.com/vishvananda/netlink"
)

// VethObserver provides per-veth packet/byte counters via netlink interface statistics.
// It is nil-safe: all methods on a nil *VethObserver are no-ops.
//
// The abstraction is kept so tc/eBPF can replace netlink stats later for
// higher resolution or per-flow counters.
type VethObserver struct {
	mu       sync.Mutex
	attached map[string]bool // veth name → attached
}

// NewVethObserver creates a new observer.
func NewVethObserver() (*VethObserver, error) {
	return &VethObserver{
		attached: make(map[string]bool),
	}, nil
}

// Attach registers a veth interface for counter collection.
func (o *VethObserver) Attach(vethName string) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.attached[vethName] {
		return fmt.Errorf("already attached to %s", vethName)
	}
	// Verify the interface exists
	if _, err := netlink.LinkByName(vethName); err != nil {
		return fmt.Errorf("interface %s not found: %w", vethName, err)
	}
	o.attached[vethName] = true
	return nil
}

// Detach unregisters a veth interface from counter collection.
func (o *VethObserver) Detach(vethName string) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	delete(o.attached, vethName)
	return nil
}

// ReadCounters reads the packet and byte counters for a veth interface
// from kernel netlink interface statistics. Returns combined rx+tx values
// as seen from the host side of the veth pair.
func (o *VethObserver) ReadCounters(vethName string) (packets, bytes uint64, err error) {
	if o == nil {
		return 0, 0, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.attached[vethName] {
		return 0, 0, fmt.Errorf("not attached to %s", vethName)
	}

	link, err := netlink.LinkByName(vethName)
	if err != nil {
		return 0, 0, fmt.Errorf("get link %s: %w", vethName, err)
	}

	stats := link.Attrs().Statistics
	if stats == nil {
		return 0, 0, nil
	}

	// Report rx+tx from the host veth perspective.
	// Host veth rx = sandbox tx (sandbox → internet).
	// Host veth tx = sandbox rx (internet → sandbox).
	packets = stats.RxPackets + stats.TxPackets
	bytes = stats.RxBytes + stats.TxBytes
	return packets, bytes, nil
}

// ReadCountersDirectional reads rx and tx counters separately.
// rx = traffic from sandbox to internet (host veth rx).
// tx = traffic from internet to sandbox (host veth tx).
func (o *VethObserver) ReadCountersDirectional(vethName string) (rxPackets, rxBytes, txPackets, txBytes uint64, err error) {
	if o == nil {
		return 0, 0, 0, 0, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.attached[vethName] {
		return 0, 0, 0, 0, fmt.Errorf("not attached to %s", vethName)
	}

	link, err := netlink.LinkByName(vethName)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("get link %s: %w", vethName, err)
	}

	stats := link.Attrs().Statistics
	if stats == nil {
		return 0, 0, 0, 0, nil
	}

	return stats.RxPackets, stats.RxBytes, stats.TxPackets, stats.TxBytes, nil
}

// Close detaches all observers.
func (o *VethObserver) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	for name := range o.attached {
		delete(o.attached, name)
	}
	return nil
}
