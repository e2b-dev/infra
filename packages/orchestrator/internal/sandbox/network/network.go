package network

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const loNS = "lo"

var hostDefaultGateway = must(getDefaultGateway())

func must[T any](obj T, err error) T {
	if err != nil {
		panic(err)
	}

	return obj
}

var blockedRanges = []string{
	"10.0.0.0/8",
	"169.254.0.0/16",
	"192.168.0.0/16",
	"172.16.0.0/12",
}

func getBlockingRule(ips *IPSlot, ipRange string) []string {
	return []string{"-p", "all", "-i", ips.TapName(), "-d", ipRange, "-j", "DROP"}
}

func getAllowRule(ips *IPSlot) []string {
	return []string{"-p", "tcp", "-i", ips.TapName(), "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}
}

func (ips *IPSlot) addBlockingRules(tables *iptables.IPTables) error {
	for _, ipRange := range blockedRanges {
		rule := getBlockingRule(ips, ipRange)

		err := tables.Append("filter", "FORWARD", rule...)
		if err != nil {
			return fmt.Errorf("error adding blocking rule: %w", err)
		}
	}

	allowRule := getAllowRule(ips)

	err := tables.Insert("filter", "FORWARD", 1, allowRule...)
	if err != nil {
		return fmt.Errorf("error adding response rule: %w", err)
	}

	return nil
}

func getDefaultGateway() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return "", fmt.Errorf("error fetching routes: %w", err)
	}

	for _, route := range routes {
		if route.Dst == nil && route.Gw != nil {
			link, linkErr := netlink.LinkByIndex(route.LinkIndex)
			if linkErr != nil {
				return "", fmt.Errorf("error fetching interface for default gateway: %w", linkErr)
			}

			return link.Attrs().Name, nil
		}
	}

	return "", fmt.Errorf("cannot find default gateway")
}

func (ips *IPSlot) CreateNetwork() error {
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
			fmt.Fprintf(os.Stderr, "error resetting network namespace back to the host namespace: %v", err)
		}

		err = hostNS.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error closing host network namespace: %v", err)
		}
	}()

	// Create NS for the sandbox
	ns, err := netns.NewNamed(ips.NamespaceID())
	if err != nil {
		return fmt.Errorf("cannot create new namespace: %w", err)
	}

	defer ns.Close()

	// Create the Veth and Vpeer
	vethAttrs := netlink.NewLinkAttrs()
	vethAttrs.Name = ips.VethName()
	veth := &netlink.Veth{
		LinkAttrs: vethAttrs,
		PeerName:  ips.VpeerName(),
	}

	err = netlink.LinkAdd(veth)
	if err != nil {
		return fmt.Errorf("error creating veth device: %w", err)
	}

	vpeer, err := netlink.LinkByName(ips.VpeerName())
	if err != nil {
		return fmt.Errorf("error finding vpeer: %w", err)
	}

	err = netlink.LinkSetUp(vpeer)
	if err != nil {
		return fmt.Errorf("error setting vpeer device up: %w", err)
	}

	ip, ipNet, err := net.ParseCIDR(ips.VpeerCIDR())
	if err != nil {
		return fmt.Errorf("error parsing vpeer CIDR: %w", err)
	}

	err = netlink.AddrAdd(vpeer, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: ipNet.Mask,
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

	vethInHost, err := netlink.LinkByName(ips.VethName())
	if err != nil {
		return fmt.Errorf("error finding veth: %w", err)
	}

	err = netlink.LinkSetUp(vethInHost)
	if err != nil {
		return fmt.Errorf("error setting veth device up: %w", err)
	}

	ip, ipNet, err = net.ParseCIDR(ips.VethCIDR())
	if err != nil {
		return fmt.Errorf("error parsing veth CIDR: %w", err)
	}

	err = netlink.AddrAdd(vethInHost, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: ipNet.Mask,
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
	tapAttrs.Name = ips.TapName()
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

	ip, ipNet, err = net.ParseCIDR(ips.TapCIDR())
	if err != nil {
		return fmt.Errorf("error parsing tap CIDR: %w", err)
	}

	err = netlink.AddrAdd(tap, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: ipNet.Mask,
		},
	})
	if err != nil {
		return fmt.Errorf("error setting address of the tap device: %w", err)
	}

	// Set NS lo device up
	lo, err := netlink.LinkByName(loNS)
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
		Gw:    net.ParseIP(ips.VethIP()),
	})
	if err != nil {
		return fmt.Errorf("error adding default NS route: %w", err)
	}

	tables, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error initializing iptables: %w", err)
	}

	// Add NAT routing rules to NS
	err = tables.Append("nat", "POSTROUTING", "-o", ips.VpeerName(), "-s", ips.NamespaceIP(), "-j", "SNAT", "--to", ips.HostIP())
	if err != nil {
		return fmt.Errorf("error creating postrouting rule to vpeer: %w", err)
	}

	err = tables.Append("nat", "PREROUTING", "-i", ips.VpeerName(), "-d", ips.HostIP(), "-j", "DNAT", "--to", ips.NamespaceIP())
	if err != nil {
		return fmt.Errorf("error creating postrouting rule from vpeer: %w", err)
	}

	err = ips.addBlockingRules(tables)
	if err != nil {
		return fmt.Errorf("error adding blocking rules: %w", err)
	}

	// Go back to original namespace
	err = netns.Set(hostNS)
	if err != nil {
		return fmt.Errorf("error setting network namespace to %s: %w", hostNS.String(), err)
	}

	// Add routing from host to FC namespace
	_, ipNet, err = net.ParseCIDR(ips.HostCIDR())
	if err != nil {
		return fmt.Errorf("error parsing host snapshot CIDR: %w", err)
	}

	err = netlink.RouteAdd(&netlink.Route{
		Gw:  net.ParseIP(ips.VpeerIP()),
		Dst: ipNet,
	})
	if err != nil {
		return fmt.Errorf("error adding route from host to FC: %w", err)
	}

	// Add host forwarding rules
	err = tables.Append("filter", "FORWARD", "-i", ips.VethName(), "-o", hostDefaultGateway, "-j", "ACCEPT")
	if err != nil {
		return fmt.Errorf("error creating forwarding rule to default gateway: %w", err)
	}

	err = tables.Append("filter", "FORWARD", "-i", hostDefaultGateway, "-o", ips.VethName(), "-j", "ACCEPT")
	if err != nil {
		return fmt.Errorf("error creating forwarding rule from default gateway: %w", err)
	}

	// Add host postrouting rules
	err = tables.Append("nat", "POSTROUTING", "-s", ips.HostCIDR(), "-o", hostDefaultGateway, "-j", "MASQUERADE")
	if err != nil {
		return fmt.Errorf("error creating postrouting rule: %w", err)
	}

	return nil
}

func (ipSlot *IPSlot) RemoveNetwork() error {
	var errs []error

	tables, err := iptables.New()
	if err != nil {
		errs = append(errs, fmt.Errorf("error initializing iptables: %w", err))
	} else {
		// Delete host forwarding rules
		err = tables.Delete("filter", "FORWARD", "-i", ipSlot.VethName(), "-o", hostDefaultGateway, "-j", "ACCEPT")
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting host forwarding rule to default gateway: %w", err))
		}

		err = tables.Delete("filter", "FORWARD", "-i", hostDefaultGateway, "-o", ipSlot.VethName(), "-j", "ACCEPT")
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting host forwarding rule from default gateway: %w", err))
		}

		// Delete host postrouting rules
		err = tables.Delete("nat", "POSTROUTING", "-s", ipSlot.HostCIDR(), "-o", hostDefaultGateway, "-j", "MASQUERADE")
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting host postrouting rule: %w", err))
		}
	}

	// Delete routing from host to FC namespace
	_, ipNet, err := net.ParseCIDR(ipSlot.HostCIDR())
	if err != nil {
		errs = append(errs, fmt.Errorf("error parsing host snapshot CIDR: %w", err))
	} else {
		err = netlink.RouteDel(&netlink.Route{
			Gw:  net.ParseIP(ipSlot.VpeerIP()),
			Dst: ipNet,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting route from host to FC: %w", err))
		}
	}

	// Delete veth device
	// We explicitly delete the veth device from the host namespace because even though deleting
	// is deleting the device there may be a race condition when creating a new veth device with
	// the same name immediately after deleting the namespace.
	veth, err := netlink.LinkByName(ipSlot.VethName())
	if err != nil {
		errs = append(errs, fmt.Errorf("error finding veth: %w", err))
	} else {
		err = netlink.LinkDel(veth)
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting veth device: %w", err))
		}
	}

	err = netns.DeleteNamed(ipSlot.NamespaceID())
	if err != nil {
		errs = append(errs, fmt.Errorf("error deleting namespace: %w", err))
	}

	return errors.Join(errs...)
}
