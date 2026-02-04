package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"

	"github.com/containernetworking/plugins/pkg/ns"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	netutils "k8s.io/utils/net"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network")

const (
	defaultHostNetworkCIDR = "10.11.0.0/16"
	defaultVrtNetworkCIDR  = "10.12.0.0/16"

	hostMask          = 32
	vrtMask           = 31                  // 2 usable ips per block (vpeer and veth)
	vrtAddressPerSlot = 1 << (32 - vrtMask) // vrt addresses per slot (vpeer and veth)

	tapMask          = 30
	tapInterfaceName = "tap0"
	tapIp            = "169.254.0.22"
	tapMAC           = "02:FC:00:00:00:05"
)

var (
	hostNetworkCIDR = getHostNetworkCIDR()
	vrtNetworkCIDR  = getVrtNetworkCIDR()
	vrtSlotsSize    = GetVrtSlotsSize()
)

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
// Vpeer receives the first IP in the block, and Veth receives the second IP. Block is calculated as (slot index * addresses per slot allocation).
// Vrt address per slot is always 2, so we can allocate /31 CIDR block for each slot.
type Slot struct {
	Key string
	Idx int

	Firewall *Firewall

	// firewallCustomRules is used to track if custom firewall rules are set for the slot and need a cleanup.
	firewallCustomRules atomic.Bool

	vPeerIp net.IP
	vEthIp  net.IP
	vrtMask net.IPMask

	tapIp   net.IP
	tapMask net.IPMask

	// HostIP is IP address for the sandbox from the host machine.
	// You can use it to make requests to the sandbox.
	hostIp   net.IP
	hostNet  *net.IPNet
	hostCIDR string

	hyperloopIP, hyperloopPort string

	// TCP firewall ports for different traffic types
	tcpFirewallHTTPPort  string // Port 80 traffic
	tcpFirewallTLSPort   string // Port 443 traffic
	tcpFirewallOtherPort string // All other traffic
}

func NewSlot(key string, idx int, config Config) (*Slot, error) {
	if idx < 1 || idx > vrtSlotsSize {
		return nil, fmt.Errorf("slot index %d is out of range [1, %d)", idx, vrtSlotsSize)
	}

	vEthIp, err := netutils.GetIndexedIP(vrtNetworkCIDR, idx*vrtAddressPerSlot)
	if err != nil {
		return nil, fmt.Errorf("failed to get veth indexed IP: %w", err)
	}

	vPeerIp, err := netutils.GetIndexedIP(vrtNetworkCIDR, idx*vrtAddressPerSlot+1)
	if err != nil {
		return nil, fmt.Errorf("failed to get vpeer indexed IP: %w", err)
	}

	vrtCIDR := fmt.Sprintf("%s/%d", vPeerIp.String(), vrtMask)
	_, vrtNet, err := net.ParseCIDR(vrtCIDR)
	if err != nil {
		return nil, fmt.Errorf("failed to parse vrt CIDR: %w", err)
	}

	hostIp, err := netutils.GetIndexedIP(hostNetworkCIDR, idx)
	if err != nil {
		return nil, fmt.Errorf("failed to get host IP: %w", err)
	}

	hostCIDR := fmt.Sprintf("%s/%d", hostIp.String(), hostMask)
	_, hostNet, err := net.ParseCIDR(hostCIDR)
	if err != nil {
		return nil, fmt.Errorf("failed to parse host CIDR: %w", err)
	}

	tapCIDR := fmt.Sprintf("%s/%d", tapIp, tapMask)
	tapIp, tapNet, err := net.ParseCIDR(tapCIDR)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tap CIDR: %w", err)
	}

	slot := &Slot{
		Key: key,
		Idx: idx,

		vPeerIp: vPeerIp,
		vEthIp:  vEthIp,
		vrtMask: vrtNet.Mask,

		tapIp:   tapIp,
		tapMask: tapNet.Mask,

		hostIp:   hostIp,
		hostNet:  hostNet,
		hostCIDR: hostCIDR,

		hyperloopIP:   config.HyperloopIPAddress,
		hyperloopPort: strconv.FormatUint(uint64(config.HyperloopProxyPort), 10),

		tcpFirewallHTTPPort:  strconv.FormatUint(uint64(config.SandboxTCPFirewallHTTPPort), 10),
		tcpFirewallTLSPort:   strconv.FormatUint(uint64(config.SandboxTCPFirewallTLSPort), 10),
		tcpFirewallOtherPort: strconv.FormatUint(uint64(config.SandboxTCPFirewallOtherPort), 10),
	}

	return slot, nil
}

func (s *Slot) VpeerName() string {
	return "eth0"
}

func (s *Slot) VpeerIP() net.IP {
	return s.vPeerIp
}

func (s *Slot) VethIP() net.IP {
	return s.vEthIp
}

func (s *Slot) VethName() string {
	return fmt.Sprintf("veth-%d", s.Idx)
}

func (s *Slot) VrtMask() net.IPMask {
	return s.vrtMask
}

func (s *Slot) HostIP() net.IP {
	return s.hostIp
}

func (s *Slot) HostIPString() string {
	return s.HostIP().String()
}

func (s *Slot) HyperloopIPString() string {
	return s.hyperloopIP
}

func (s *Slot) HostMask() net.IPMask {
	return s.hostNet.Mask
}

func (s *Slot) HostNet() *net.IPNet {
	return s.hostNet
}

func (s *Slot) HostCIDR() string {
	return s.hostCIDR
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

func (s *Slot) TapIP() net.IP {
	return s.tapIp
}

func (s *Slot) TapIPString() string {
	return s.tapIp.String()
}

func (s *Slot) TapMask() int {
	return tapMask
}

func (s *Slot) TapMaskString() string {
	mask := net.CIDRMask(s.TapMask(), 32)

	return net.IP(mask).String()
}

func (s *Slot) TapCIDR() net.IPMask {
	return s.tapMask
}

func (s *Slot) TapMAC() string {
	return tapMAC
}

func (s *Slot) InitializeFirewall() error {
	if s.Firewall != nil {
		return fmt.Errorf("firewall is already initialized for slot %s", s.Key)
	}

	fw, err := NewFirewall(s.TapName(), s.HyperloopIPString())
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

func (s *Slot) ConfigureInternet(ctx context.Context, network *orchestrator.SandboxNetworkConfig) (e error) {
	_, span := tracer.Start(ctx, "slot-internet-configure", trace.WithAttributes(
		attribute.String("namespace_id", s.NamespaceID()),
	))
	defer span.End()

	egress := network.GetEgress()
	if len(egress.GetAllowedCidrs()) == 0 && len(egress.GetDeniedCidrs()) == 0 && len(egress.GetAllowedDomains()) == 0 {
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
		for _, cidr := range network.GetEgress().GetAllowedCidrs() {
			err := s.Firewall.AddAllowedCIDR(cidr)
			if err != nil {
				return fmt.Errorf("error setting firewall rules: %w", err)
			}
		}

		for _, cidr := range network.GetEgress().GetDeniedCidrs() {
			err := s.Firewall.AddDeniedCIDR(cidr)
			if err != nil {
				return fmt.Errorf("error setting firewall rules: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed execution in network namespace '%s': %w", s.NamespaceID(), err)
	}

	return nil
}

func (s *Slot) ResetInternet(ctx context.Context) error {
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
		err := s.Firewall.Reset()
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

	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		log.Fatalf("Failed to parse network CIDR %s: %v", cidr, err)
	}

	log.Println("Using host network cidr", "cidr", cidr)

	return subnet
}

func getVrtNetworkCIDR() *net.IPNet {
	cidr := env.GetEnv("SANDBOXES_VRT_NETWORK_CIDR", defaultVrtNetworkCIDR)

	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		log.Fatalf("Failed to parse network CIDR %s: %v", cidr, err)
	}

	log.Printf("Using vrt network cidr %s", cidr)

	return subnet
}

func GetVrtSlotsSize() int {
	ones, _ := getVrtNetworkCIDR().Mask.Size()

	// total IPs in the CIDR block
	totalIPs := 1 << (32 - ones)

	// total slots that we can allocate
	// we need to divide total IPs by number of addresses per slot (vpeer and veth)
	// then we subtract the number of addresses so it will not overflow, because we are adding them incrementally by slot index
	totalSlots := (totalIPs / vrtAddressPerSlot) - vrtAddressPerSlot

	log.Printf("Using network slot size: %d", totalSlots)

	return totalSlots
}
