package v2

import (
	"fmt"
	"net"
	"sync"
)

// EgressProfile binds sandboxes to a specific egress identity (source IP).
// Multiple sandboxes can share the same profile (customer-shared mode).
type EgressProfile struct {
	ID        string
	OwnerType string // "customer" or "sandbox"
	OwnerID   string
	Region    string
	Mode      string // "customer-shared" | "sandbox-dedicated"

	// BackendType selects the egress mechanism.
	// "gateway"   — route through WireGuard to egress gateway for SNAT
	// "cloud_nat" — use cloud provider NAT (production)
	// "direct"    — no special egress, use host's default route
	BackendType string

	// PublicIPSet are the sticky outbound IPs visible to external services.
	PublicIPSet []net.IP

	// GatewayAddr is the WireGuard IP of the egress gateway (e.g., 10.99.0.1).
	GatewayAddr net.IP

	// GatewaySNATIP is the IP the gateway will SNAT traffic to.
	GatewaySNATIP net.IP

	// FwMark is the packet mark used for policy routing.
	FwMark uint32

	// RouteTableID is the ip rule/route table for this profile.
	RouteTableID int

	// WgDevice is the WireGuard interface to route through (e.g., "wg0").
	WgDevice string
}

// EgressManager manages EgressProfiles and their routing setup.
type EgressManager struct {
	profiles map[string]*EgressProfile
	mu       sync.RWMutex
}

func NewEgressManager() *EgressManager {
	return &EgressManager{
		profiles: make(map[string]*EgressProfile),
	}
}

// Register adds a profile to the manager.
func (m *EgressManager) Register(profile *EgressProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.profiles[profile.ID]; exists {
		return fmt.Errorf("egress profile %s already registered", profile.ID)
	}

	m.profiles[profile.ID] = profile
	return nil
}

// GetProfile returns a profile by ID.
func (m *EgressManager) GetProfile(id string) *EgressProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profiles[id]
}

// SetupRouting configures policy routing for a profile:
//
//	ip rule add fwmark <fwmark> lookup <tableID>
//	ip route add default via <gatewayAddr> dev <wgDevice> table <tableID>
//
// Also ensures net.ipv4.conf.all.src_valid_mark=1 which is required
// for the kernel to re-route forwarded packets based on fwmark.
func (m *EgressManager) SetupRouting(profile *EgressProfile) error {
	if profile.BackendType == "direct" {
		return nil // no policy routing needed
	}

	if profile.GatewayAddr == nil {
		return fmt.Errorf("profile %s has no gateway address", profile.ID)
	}

	// Required for fwmark-based rerouting of forwarded traffic.
	if err := EnsureSrcValidMark(); err != nil {
		return fmt.Errorf("ensure src_valid_mark: %w", err)
	}

	return SetupPolicyRoute(
		profile.FwMark,
		profile.RouteTableID,
		profile.GatewayAddr,
		profile.WgDevice,
	)
}

// TeardownRouting removes policy routing for a profile.
func (m *EgressManager) TeardownRouting(profile *EgressProfile) error {
	if profile.BackendType == "direct" {
		return nil
	}

	return TeardownPolicyRoute(profile.FwMark, profile.RouteTableID)
}

// AssignSlot marks a slot's outbound traffic with the profile's fwmark.
func (m *EgressManager) AssignSlot(hf *HostFirewall, slotV2 *SlotV2, profileID string) error {
	profile := m.GetProfile(profileID)
	if profile == nil {
		return fmt.Errorf("egress profile %s not found", profileID)
	}

	slotV2.EgressProfileID = profileID

	// Mark traffic from this slot's veth with the profile's fwmark
	return SetupFwmarkInNftables(hf, slotV2.Slot.VethName(), profile.FwMark)
}

// UnassignSlot removes the fwmark rule for a slot.
func (m *EgressManager) UnassignSlot(hf *HostFirewall, slotV2 *SlotV2) error {
	return RemoveFwmarkInNftables(hf, slotV2.Slot.VethName())
}

// Close tears down routing for all profiles.
func (m *EgressManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, profile := range m.profiles {
		if err := m.TeardownRouting(profile); err != nil {
			errs = append(errs, err)
		}
	}
	m.profiles = make(map[string]*EgressProfile)

	if len(errs) > 0 {
		return fmt.Errorf("teardown egress profiles: %v", errs)
	}
	return nil
}

// DefaultEgressProfile creates a direct (no gateway) profile for testing.
func DefaultEgressProfile() *EgressProfile {
	return &EgressProfile{
		ID:          "default",
		OwnerType:   "system",
		OwnerID:     "default",
		Region:      "local",
		Mode:        "customer-shared",
		BackendType: "direct",
	}
}
