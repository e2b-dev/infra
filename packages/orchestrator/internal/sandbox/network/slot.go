package network

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"

	"github.com/containernetworking/plugins/pkg/ns"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	netutils "k8s.io/utils/net"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	// This is the maximum number of IP addresses that can be allocated.
	slotsSize = 256 * 256 / vrtAddressPerSlot

	defaultHostNetworkCIDR = "10.11.0.0/16"
	defaultVrtNetworkCIDR  = "10.12.0.0/16"

	hostMask          = 32
	vrtMask           = 31 // 2 usable Ips per block (Vpeer and Veth ips)
	vrtAddressPerSlot = 1 << (32 - vrtMask)

	tapMask          = 30
	tapInterfaceName = "tap0"
	tapIp            = "169.254.0.22"
	tapMAC           = "02:FC:00:00:00:05"
)

var hostNetworkCIDR = getHostNetworkCIDR()
var vrtNetworkCIDR = getVrtNetworkCIDR()

// Slot network allocation
//
// For each slot, we allocate three IP addresses:
// Host IP - used to access the sandbox from the host machine
// Vpeer and Veth IPs - used by the sandbox to communicate with the host
//
// Host default namespace creates a /16 CIDR block for the host IPs.
// Slot with Idx 1 will receive 10.11.0.1 and so on. Its allocated incrementally by slot Idx.
// Host mask is /32 because we only use one IP per slot.
//
// Vrt addresses (vpeer and veth) are allocated from a /31 CIDR block so we can use CIDR for network link routing.
// By default, they are using 10.12.0.0/16 CIDR block, that can be configured via environment variable.
// Vpeer receives the first IP in the block, and Veth receives the second IP. Block is calculated as (slot index * vrtAddressPerSlot).
// Vrt address per slot is always 2, so we can allocate /31 CIDR block for each slot.
//
// With default CIDR /16 we can allocate up to 32 768 slots with following calculation (256 * 256) / 2
// We are cutting CIDR in half because we need 2 IPs per slot (Vpeer and Veth).
type Slot struct {
	Key string
	Idx int

	Firewall *Firewall
	// firewallCustomRules is used to track if custom firewall rules are set for the slot and need a cleanup.
	firewallCustomRules atomic.Bool
}

func NewSlot(key string, idx int) *Slot {
	return &Slot{
		Key: key,
		Idx: idx,
	}
}

func (s *Slot) VpeerName() string {
	return "eth0"
}

func (s *Slot) VpeerIP() net.IP {
	ip, err := netutils.GetIndexedIP(vrtNetworkCIDR, s.Idx*vrtAddressPerSlot)
	if err != nil {
		zap.L().Error("Failed to get indexed IP", zap.Int("index", s.Idx), zap.Error(err))
	}
	return ip
}

func (s *Slot) VethIP() net.IP {
	ip, err := netutils.GetIndexedIP(vrtNetworkCIDR, s.Idx*vrtAddressPerSlot+1)
	if err != nil {
		zap.L().Error("Failed to get indexed IP", zap.Int("index", s.Idx), zap.Error(err))
	}
	return ip
}

func (s *Slot) VethName() string {
	return fmt.Sprintf("veth-%d", s.Idx)
}

func (s *Slot) VrtCIDR() string {
	return fmt.Sprintf("%s/%d", s.VpeerIP().String(), vrtMask)
}

// HostIP is IP address for the sandbox from the host machine.
// You can use it to make requests to the sandbox.
func (s *Slot) HostIP() net.IP {
	ip, err := netutils.GetIndexedIP(hostNetworkCIDR, s.Idx)
	if err != nil {
		zap.L().Error("Failed to get indexed IP", zap.Int("index", s.Idx), zap.Error(err))
	}

	return ip
}

func (s *Slot) HostIPString() string {
	return s.HostIP().String()
}

func (s *Slot) HostCIDR() string {
	return fmt.Sprintf("%s/%d", s.HostIPString(), hostMask)
}

func (s *Slot) NamespaceIP() string {
	return "169.254.0.21"
}

func (s *Slot) NamespaceID() string {
	return fmt.Sprintf("ns-%d", s.Idx)
}

func (s *Slot) TapName() string {
	return tapInterfaceName
}

func (s *Slot) TapIP() string {
	return tapIp
}

func (s *Slot) TapMask() int {
	return tapMask
}

func (s *Slot) TapMaskString() string {
	mask := net.CIDRMask(s.TapMask(), 32)
	return net.IP(mask).String()
}

func (s *Slot) TapCIDR() string {
	return fmt.Sprintf("%s/%d", s.TapIP(), s.TapMask())
}

func (s *Slot) TapMAC() string {
	return tapMAC
}

func (s *Slot) InitializeFirewall() error {
	if s.Firewall != nil {
		return fmt.Errorf("firewall is already initialized for slot %s", s.Key)
	}

	fw, err := NewFirewall(s.TapName())
	if err != nil {
		return fmt.Errorf("error initializing firewall: %w", err)
	}
	s.Firewall = fw

	return nil
}

func (s *Slot) CloseFirewall() error {
	if s.Firewall == nil {
		return nil
	}

	if err := s.Firewall.Close(); err != nil {
		return fmt.Errorf("error closing firewall: %w", err)
	}
	s.Firewall = nil

	return nil
}

func (s *Slot) ConfigureInternet(ctx context.Context, tracer trace.Tracer, allowInternet bool) (e error) {
	_, span := tracer.Start(ctx, "slot-internet-configure", trace.WithAttributes(
		attribute.String("namespace_id", s.NamespaceID()),
		attribute.Bool("allow_internet", allowInternet),
	))
	defer span.End()

	if allowInternet {
		// Internet access is allowed by default.
		return nil
	}

	s.firewallCustomRules.Store(true)

	n, err := ns.GetNS(filepath.Join(netNamespacesDir, s.NamespaceID()))
	if err != nil {
		return fmt.Errorf("failed to get slot network namespace '%s': %w", s.NamespaceID(), err)
	}
	defer n.Close()

	err = n.Do(func(_ ns.NetNS) error {
		err = s.Firewall.AddBlockedIP("0.0.0.0/0")
		if err != nil {
			return fmt.Errorf("error setting firewall rules: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed execution in network namespace '%s': %w", s.NamespaceID(), err)
	}

	return nil
}

func (s *Slot) ResetInternet(ctx context.Context, tracer trace.Tracer) error {
	_, span := tracer.Start(ctx, "slot-internet-reset", trace.WithAttributes(
		attribute.String("namespace_id", s.NamespaceID()),
	))
	defer span.End()

	if !s.firewallCustomRules.CompareAndSwap(true, false) {
		return nil
	}

	n, err := ns.GetNS(filepath.Join(netNamespacesDir, s.NamespaceID()))
	if err != nil {
		return fmt.Errorf("failed to get slot network namespace '%s': %w", s.NamespaceID(), err)
	}
	defer n.Close()

	err = n.Do(func(_ ns.NetNS) error {
		err := s.Firewall.ResetAllCustom()
		if err != nil {
			return fmt.Errorf("error cleaning firewall rules: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed execution in network namespace '%s': %w", s.NamespaceID(), err)
	}

	return nil
}

func getHostNetworkCIDR() *net.IPNet {
	cidr := env.GetEnv("SANDBOXES_HOST_NETWORK_CIDR", defaultHostNetworkCIDR)

	zap.L().Info("Using host network cidr", zap.String("cidr", cidr))

	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		zap.L().Fatal("Failed to parse host network CIDR", zap.String("cidr", cidr), zap.Error(err))
	}

	return subnet
}

func getVrtNetworkCIDR() *net.IPNet {
	cidr := env.GetEnv("SANDBOXES_VRT_NETWORK_CIDR", defaultVrtNetworkCIDR)

	zap.L().Info("Using vrt network cidr", zap.String("cidr", cidr))

	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		zap.L().Fatal("Failed to parse network CIDR", zap.String("cidr", cidr), zap.Error(err))
	}

	return subnet
}
