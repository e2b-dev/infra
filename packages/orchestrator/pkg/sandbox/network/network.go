//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// installGlobalSandboxRules installs the host-side iptables rules that are
// identical for every sandbox on this node. Previously these were appended
// per sandbox with `-i veth-N`; since they all match dst IP
// OrchestratorInSandboxIPAddress (only routable from inside a sandbox netns
// and never seen on the host's external NIC), dropping the inbound-interface
// constraint is safe and lets us install them once at orchestrator boot
// instead of 3× per sandbox.
func installGlobalSandboxRules(config Config) error {
	tables, err := iptables.New()
	if err != nil {
		return fmt.Errorf("init iptables: %w", err)
	}

	dst := config.OrchestratorInSandboxIPAddress
	rules := [][]string{
		{"-p", "tcp", "-d", dst, "--dport", "111", "-j", "REDIRECT", "--to-port", strconv.Itoa(int(config.PortmapperPort))},
		{"-p", "tcp", "-d", dst, "--dport", "2049", "-j", "REDIRECT", "--to-port", strconv.Itoa(int(config.NFSProxyPort))},
		{"-p", "tcp", "-d", dst, "--dport", "80", "-j", "REDIRECT", "--to-port", strconv.Itoa(int(config.HyperloopProxyPort))},
	}
	for _, args := range rules {
		if err := tables.AppendUnique("nat", "PREROUTING", args...); err != nil {
			return fmt.Errorf("install global rule %v: %w", args, err)
		}
	}
	return nil
}

func (s *Slot) CreateNetwork(ctx context.Context) error {
	// Prevent thread changes so we can safely manipulate with namespaces
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Save the original (host) namespace and restore it upon function exit
	hostNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("cannot get current (host) namespace: %w", err)
	}

	defer func() {
		err = netns.Set(hostNS)
		if err != nil {
			logger.L().Error(ctx, "error resetting network namespace back to the host namespace", zap.Error(err))
		}

		err = hostNS.Close()
		if err != nil {
			logger.L().Error(ctx, "error closing host network namespace", zap.Error(err))
		}
	}()

	// Create NS for the sandbox
	ns, err := netns.NewNamed(s.NamespaceID())
	if err != nil {
		return fmt.Errorf("cannot create new namespace: %w", err)
	}

	defer ns.Close()

	// Create the Veth and Vpeer
	vethAttrs := netlink.NewLinkAttrs()
	vethAttrs.Name = s.VethName()
	veth := &netlink.Veth{
		LinkAttrs: vethAttrs,
		PeerName:  s.VpeerName(),
	}

	err = netlink.LinkAdd(veth)
	if err != nil {
		return fmt.Errorf("error creating veth device: %w", err)
	}

	vpeer, err := netlink.LinkByName(s.VpeerName())
	if err != nil {
		return fmt.Errorf("error finding vpeer: %w", err)
	}

	err = netlink.LinkSetUp(vpeer)
	if err != nil {
		return fmt.Errorf("error setting vpeer device up: %w", err)
	}

	err = netlink.AddrAdd(vpeer, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   s.VpeerIP(),
			Mask: s.VrtMask(),
		},
	})
	if err != nil {
		return fmt.Errorf("error adding vpeer device address: %w", err)
	}

	// Move Veth device to the host NS
	err = netlink.LinkSetNsFd(veth, int(hostNS))
	if err != nil {
		return fmt.Errorf("error moving veth device to the host namespace: %w", err)
	}

	err = netns.Set(hostNS)
	if err != nil {
		return fmt.Errorf("error setting network namespace: %w", err)
	}

	vethInHost, err := netlink.LinkByName(s.VethName())
	if err != nil {
		return fmt.Errorf("error finding veth: %w", err)
	}

	err = netlink.LinkSetUp(vethInHost)
	if err != nil {
		return fmt.Errorf("error setting veth device up: %w", err)
	}

	err = netlink.AddrAdd(vethInHost, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   s.VethIP(),
			Mask: s.VrtMask(),
		},
	})
	if err != nil {
		return fmt.Errorf("error adding veth device address: %w", err)
	}

	err = netns.Set(ns)
	if err != nil {
		return fmt.Errorf("error setting network namespace to %s: %w", ns.String(), err)
	}

	// Create Tap device for FC in NS
	tapAttrs := netlink.NewLinkAttrs()
	tapAttrs.Name = s.TapName()
	tapAttrs.Namespace = ns
	tap := &netlink.Tuntap{
		Mode:      netlink.TUNTAP_MODE_TAP,
		LinkAttrs: tapAttrs,
	}

	err = netlink.LinkAdd(tap)
	if err != nil {
		return fmt.Errorf("error creating tap device: %w", err)
	}

	err = netlink.LinkSetUp(tap)
	if err != nil {
		return fmt.Errorf("error setting tap device up: %w", err)
	}

	err = netlink.AddrAdd(tap, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   s.TapIP(),
			Mask: s.TapCIDR(),
		},
	})
	if err != nil {
		return fmt.Errorf("error setting address of the tap device: %w", err)
	}

	// Set NS lo device up
	lo, err := netlink.LinkByName(loopbackInterface)
	if err != nil {
		return fmt.Errorf("error finding lo: %w", err)
	}

	err = netlink.LinkSetUp(lo)
	if err != nil {
		return fmt.Errorf("error setting lo device up: %w", err)
	}

	// Add NS default route
	err = netlink.RouteAdd(&netlink.Route{
		Scope: netlink.SCOPE_UNIVERSE,
		Gw:    s.VethIP(),
	})
	if err != nil {
		return fmt.Errorf("error adding default NS route: %w", err)
	}

	tables, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error initializing iptables: %w", err)
	}

	// Add NAT routing rules to NS
	err = tables.Append("nat", "POSTROUTING", "-o", s.VpeerName(), "-s", s.NamespaceIP(), "-j", "SNAT", "--to", s.HostIPString())
	if err != nil {
		return fmt.Errorf("error creating postrouting rule to vpeer: %w", err)
	}

	err = tables.Append("nat", "PREROUTING", "-i", s.VpeerName(), "-d", s.HostIPString(), "-j", "DNAT", "--to", s.NamespaceIP())
	if err != nil {
		return fmt.Errorf("error creating postrouting rule from vpeer: %w", err)
	}

	err = s.InitializeFirewall()
	if err != nil {
		return fmt.Errorf("error initializing slot firewall: %w", err)
	}

	// Go back to original namespace
	err = netns.Set(hostNS)
	if err != nil {
		return fmt.Errorf("error setting network namespace to %s: %w", hostNS.String(), err)
	}

	// Add routing from host to FC namespace
	err = netlink.RouteAdd(&netlink.Route{
		Gw:  s.VpeerIP(),
		Dst: s.HostNet(),
	})
	if err != nil {
		return fmt.Errorf("error adding route from host to FC: %w", err)
	}

	// Batch the per-sandbox host-side iptables work so a single
	// `iptables-restore --noflush` invocation handles all rules at once.
	// On iptables-nft this avoids one full table rewrite per rule (the
	// O(N²) cost that grows with the rule count on the host).
	//
	// The hyperloop / portmapper / NFS-proxy REDIRECT rules are *not*
	// added here: they match on dst = OrchestratorInSandboxIPAddress and
	// are installed once at orchestrator boot by installGlobalSandboxRules
	// rather than per-sandbox.
	hostRules := NewRuleSet()

	hostRules.Append("filter", "FORWARD", "-i", s.VethName(), "-o", defaultGateway, "-j", "ACCEPT")
	hostRules.Append("filter", "FORWARD", "-i", defaultGateway, "-o", s.VethName(), "-j", "ACCEPT")
	hostRules.Append("nat", "POSTROUTING", "-s", s.HostCIDR(), "-o", defaultGateway, "-j", "MASQUERADE")

	// Egress proxy contributes its per-sandbox PREROUTING REDIRECTs into
	// the same RuleSet so everything lands in one syscall.
	if err := s.egressProxy.OnSlotCreate(s, hostRules); err != nil {
		return err
	}

	if err := hostRules.Apply(ctx); err != nil {
		return fmt.Errorf("error applying host iptables rules for slot: %w", err)
	}

	return nil
}

func (s *Slot) RemoveNetwork(ctx context.Context) error {
	var errs []error

	err := s.CloseFirewall()
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing firewall: %w", err))
	}

	// Mirror of CreateNetwork's host-side batched setup: collect every
	// per-sandbox rule we want gone into a single RuleSet and flush via
	// iptables-restore --noflush. Global PREROUTING REDIRECTs are not
	// touched: they live for the orchestrator's lifetime.
	hostRules := NewRuleSet()
	hostRules.Delete("filter", "FORWARD", "-i", s.VethName(), "-o", defaultGateway, "-j", "ACCEPT")
	hostRules.Delete("filter", "FORWARD", "-i", defaultGateway, "-o", s.VethName(), "-j", "ACCEPT")
	hostRules.Delete("nat", "POSTROUTING", "-s", s.HostCIDR(), "-o", defaultGateway, "-j", "MASQUERADE")

	if err := s.egressProxy.OnSlotDelete(s, hostRules); err != nil {
		errs = append(errs, err)
	}

	// Teardown happens on best-effort paths (e.g. a partial CreateNetwork
	// may leave some rules missing). Fall back to per-rule shellouts so a
	// missing rule does not abort the whole removal.
	if err := hostRules.ApplyBestEffort(ctx); err != nil {
		errs = append(errs, fmt.Errorf("error applying host iptables removals for slot: %w", err))
	}

	// Delete routing from host to FC namespace
	err = netlink.RouteDel(&netlink.Route{
		Gw:  s.VpeerIP(),
		Dst: s.HostNet(),
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("error deleting route from host to FC: %w", err))
	}

	// Delete veth device
	// We explicitly delete the veth device from the host namespace because even though deleting
	// is deleting the device there may be a race condition when creating a new veth device with
	// the same name immediately after deleting the namespace.
	veth, err := netlink.LinkByName(s.VethName())
	if err != nil {
		errs = append(errs, fmt.Errorf("error finding veth: %w", err))
	} else {
		err = netlink.LinkDel(veth)
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting veth device: %w", err))
		}
	}

	err = netns.DeleteNamed(s.NamespaceID())
	if err != nil {
		errs = append(errs, fmt.Errorf("error deleting namespace: %w", err))
	}

	return errors.Join(errs...)
}
