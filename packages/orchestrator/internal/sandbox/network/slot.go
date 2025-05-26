package network

import (
	"context"
	"fmt"
	"net"
	"path/filepath"

	"github.com/containernetworking/plugins/pkg/ns"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// We are using a more debuggable IP address allocation for now that only covers 255 addresses.
const (
	octetSize = 256
	octetMax  = octetSize - 1
	// This is the maximum number of IP addresses that can be allocated.
	slotsSize = octetSize * octetSize

	hostMask = 32
	vMask    = 30
	tapMask  = 30
)

type Slot struct {
	Key string
	Idx int

	Firewall *Firewall
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

func (s *Slot) getOctets() (int, int) {
	rem := s.Idx % octetSize
	octet := (s.Idx - rem) / octetSize

	return octet, rem
}

func (s *Slot) VpeerIP() string {
	firstOctet, secondOctet := s.getOctets()

	return fmt.Sprintf("10.%d.%d.2", firstOctet, secondOctet)
}

func (s *Slot) VethIP() string {
	firstOctet, secondOctet := s.getOctets()

	return fmt.Sprintf("10.%d.%d.1", firstOctet, secondOctet)
}

func (s *Slot) VMask() int {
	return vMask
}

func (s *Slot) VethName() string {
	return fmt.Sprintf("veth-%d", s.Idx)
}

func (s *Slot) VethCIDR() string {
	return fmt.Sprintf("%s/%d", s.VethIP(), s.VMask())
}

func (s *Slot) VpeerCIDR() string {
	return fmt.Sprintf("%s/%d", s.VpeerIP(), s.VMask())
}

func (s *Slot) HostCIDR() string {
	return fmt.Sprintf("%s/%d", s.HostIP(), s.HostMask())
}

func (s *Slot) HostMask() int {
	return hostMask
}

// IP address for the sandbox from the host machine.
// You can use it to make requests to the sandbox.
func (s *Slot) HostIP() string {
	firstOctet, secondOctet := s.getOctets()

	return fmt.Sprintf("192.168.%d.%d", firstOctet, secondOctet)
}

func (s *Slot) NamespaceIP() string {
	return "169.254.0.21"
}

func (s *Slot) NamespaceID() string {
	return fmt.Sprintf("ns-%d", s.Idx)
}

func (s *Slot) TapName() string {
	return "tap0"
}

func (s *Slot) TapIP() string {
	return "169.254.0.22"
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
	return "02:FC:00:00:00:05"
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

	n, err := ns.GetNS(filepath.Join(NetNamespacesDir, s.NamespaceID()))
	if err != nil {
		return fmt.Errorf("failed to get slot network namespace '%s': %w", s.NamespaceID(), err)
	}
	defer n.Close()

	err = n.Do(func(_ ns.NetNS) error {
		if !allowInternet {
			err = s.Firewall.AddBlockedIP("0.0.0.0/0")
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

func (s *Slot) ResetInternet(ctx context.Context, tracer trace.Tracer) error {
	_, span := tracer.Start(ctx, "slot-internet-reset", trace.WithAttributes(
		attribute.String("namespace_id", s.NamespaceID()),
	))
	defer span.End()

	n, err := ns.GetNS(filepath.Join(NetNamespacesDir, s.NamespaceID()))
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
