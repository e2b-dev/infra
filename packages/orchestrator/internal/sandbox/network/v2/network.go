package v2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// CreateNetworkV2 sets up the full network stack for a v2 slot.
// It reuses the same netlink setup pattern as v1 (namespace, veth, tap, routes)
// but replaces all iptables calls with nftables.
//
// Compared to v1:
//   - In-namespace NAT: nftables SetupNamespaceNAT() replaces 2 iptables rules
//   - Host firewall: hf.AddSlot() replaces 8 iptables rules with 2 set element adds
//   - eBPF: observer.Attach() adds optional per-veth counters (best-effort)
func CreateNetworkV2(ctx context.Context, slot *network.Slot, slotV2 *SlotV2,
	hf *HostFirewall, observer *VethObserver) error {

	// Prevent thread changes so we can safely manipulate namespaces
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Save the original (host) namespace
	hostNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("cannot get current (host) namespace: %w", err)
	}
	defer func() {
		if err := netns.Set(hostNS); err != nil {
			logger.L().Error(ctx, "error resetting network namespace back to host", zap.Error(err))
		}
		hostNS.Close()
	}()

	// --- Create namespace ---
	ns, err := netns.NewNamed(slot.NamespaceID())
	if err != nil {
		return fmt.Errorf("cannot create new namespace: %w", err)
	}
	defer ns.Close()

	// --- Create veth pair (inside new namespace, then move veth to host) ---
	vethAttrs := netlink.NewLinkAttrs()
	vethAttrs.Name = slot.VethName()
	veth := &netlink.Veth{
		LinkAttrs: vethAttrs,
		PeerName:  slot.VpeerName(),
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("error creating veth device: %w", err)
	}

	vpeer, err := netlink.LinkByName(slot.VpeerName())
	if err != nil {
		return fmt.Errorf("error finding vpeer: %w", err)
	}

	if err := netlink.LinkSetUp(vpeer); err != nil {
		return fmt.Errorf("error setting vpeer device up: %w", err)
	}

	if err := netlink.AddrAdd(vpeer, &netlink.Addr{
		IPNet: &net.IPNet{IP: slot.VpeerIP(), Mask: slot.VrtMask()},
	}); err != nil {
		return fmt.Errorf("error adding vpeer device address: %w", err)
	}

	// Move veth to host namespace
	if err := netlink.LinkSetNsFd(veth, int(hostNS)); err != nil {
		return fmt.Errorf("error moving veth device to host namespace: %w", err)
	}

	if err := netns.Set(hostNS); err != nil {
		return fmt.Errorf("error setting network namespace: %w", err)
	}

	vethInHost, err := netlink.LinkByName(slot.VethName())
	if err != nil {
		return fmt.Errorf("error finding veth: %w", err)
	}

	if err := netlink.LinkSetUp(vethInHost); err != nil {
		return fmt.Errorf("error setting veth device up: %w", err)
	}

	if err := netlink.AddrAdd(vethInHost, &netlink.Addr{
		IPNet: &net.IPNet{IP: slot.VethIP(), Mask: slot.VrtMask()},
	}); err != nil {
		return fmt.Errorf("error adding veth device address: %w", err)
	}

	// Switch back to sandbox namespace for tap setup
	if err := netns.Set(ns); err != nil {
		return fmt.Errorf("error setting network namespace to %s: %w", ns.String(), err)
	}

	// --- Create TAP device for Firecracker ---
	tapAttrs := netlink.NewLinkAttrs()
	tapAttrs.Name = slot.TapName()
	tapAttrs.Namespace = ns
	tap := &netlink.Tuntap{
		Mode:      netlink.TUNTAP_MODE_TAP,
		LinkAttrs: tapAttrs,
	}

	if err := netlink.LinkAdd(tap); err != nil {
		return fmt.Errorf("error creating tap device: %w", err)
	}

	if err := netlink.LinkSetUp(tap); err != nil {
		return fmt.Errorf("error setting tap device up: %w", err)
	}

	if err := netlink.AddrAdd(tap, &netlink.Addr{
		IPNet: &net.IPNet{IP: slot.TapIP(), Mask: slot.TapCIDR()},
	}); err != nil {
		return fmt.Errorf("error setting address of the tap device: %w", err)
	}

	// --- Loopback ---
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("error finding lo: %w", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return fmt.Errorf("error setting lo device up: %w", err)
	}

	// --- Default route inside namespace ---
	if err := netlink.RouteAdd(&netlink.Route{
		Scope: netlink.SCOPE_UNIVERSE,
		Gw:    slot.VethIP(),
	}); err != nil {
		return fmt.Errorf("error adding default NS route: %w", err)
	}

	// --- In-namespace NAT (replaces 2 iptables rules) ---
	// We need a fresh nftables conn inside this namespace context
	nsConn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return fmt.Errorf("nftables conn in namespace: %w", err)
	}

	// The v1 Firewall creates a table named "slot-firewall" — reuse it
	nsTable := nsConn.AddTable(&nftables.Table{
		Name:   "slot-firewall",
		Family: nftables.TableFamilyINet,
	})

	if err := SetupNamespaceNAT(nsConn, nsTable, slot.VpeerName(), slot.HostIPString(), slot.NamespaceIP()); err != nil {
		nsConn.CloseLasting()
		return fmt.Errorf("setup namespace NAT: %w", err)
	}
	nsConn.CloseLasting()

	// --- Initialize in-namespace firewall (reuse v1 Firewall) ---
	if err := slot.InitializeFirewall(); err != nil {
		return fmt.Errorf("error initializing slot firewall: %w", err)
	}

	// --- Switch back to host namespace ---
	if err := netns.Set(hostNS); err != nil {
		return fmt.Errorf("error setting network namespace to %s: %w", hostNS.String(), err)
	}

	// --- Host route to namespace ---
	if err := netlink.RouteAdd(&netlink.Route{
		Gw:  slot.VpeerIP(),
		Dst: slot.HostNet(),
	}); err != nil {
		return fmt.Errorf("error adding route from host to FC: %w", err)
	}

	// --- Host firewall: add slot to sets (replaces 8 iptables rules) ---
	if err := hf.AddSlot(slotV2); err != nil {
		return fmt.Errorf("error adding slot to host firewall: %w", err)
	}

	// --- eBPF observability (best-effort) ---
	if observer != nil {
		if err := observer.Attach(slot.VethName()); err != nil {
			logger.L().Warn(ctx, "failed to attach eBPF observer (non-fatal)", zap.Error(err))
		}
	}

	return nil
}

// RemoveNetworkV2 tears down the network for a v2 slot.
func RemoveNetworkV2(ctx context.Context, slot *network.Slot, slotV2 *SlotV2,
	hf *HostFirewall, observer *VethObserver) error {

	var errs []error

	// Close in-namespace firewall
	if err := slot.CloseFirewall(); err != nil {
		errs = append(errs, fmt.Errorf("error closing firewall: %w", err))
	}

	// Remove from host firewall sets
	if err := hf.RemoveSlot(slotV2); err != nil {
		errs = append(errs, fmt.Errorf("error removing slot from host firewall: %w", err))
	}

	// Detach eBPF observer
	if observer != nil {
		if err := observer.Detach(slot.VethName()); err != nil {
			errs = append(errs, fmt.Errorf("error detaching eBPF observer: %w", err))
		}
	}

	// Delete host route
	if err := netlink.RouteDel(&netlink.Route{
		Gw:  slot.VpeerIP(),
		Dst: slot.HostNet(),
	}); err != nil {
		errs = append(errs, fmt.Errorf("error deleting route from host to FC: %w", err))
	}

	// Delete veth device explicitly (prevents race on reuse)
	vethLink, err := netlink.LinkByName(slot.VethName())
	if err != nil {
		errs = append(errs, fmt.Errorf("error finding veth: %w", err))
	} else {
		if err := netlink.LinkDel(vethLink); err != nil {
			errs = append(errs, fmt.Errorf("error deleting veth device: %w", err))
		}
	}

	// Delete namespace
	if err := netns.DeleteNamed(slot.NamespaceID()); err != nil {
		errs = append(errs, fmt.Errorf("error deleting namespace: %w", err))
	}

	return errors.Join(errs...)
}
