package v2

import (
	"fmt"
	"net"
	"os"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
)

// EnsureSrcValidMark sets net.ipv4.conf.all.src_valid_mark=1 which is required
// for fwmark-based policy routing of forwarded traffic. Without this, the kernel
// may not re-route forwarded packets based on marks set in prerouting.
func EnsureSrcValidMark() error {
	return os.WriteFile("/proc/sys/net/ipv4/conf/all/src_valid_mark", []byte("1"), 0644)
}

// SetupPolicyRoute creates a policy routing rule and route table entry
// for a given fwmark. Traffic marked with fwmark will be routed through
// the specified gateway device.
//
// Equivalent to:
//
//	ip rule add fwmark <fwmark> lookup <tableID>
//	ip route add default via <gw> dev <dev> table <tableID>
func SetupPolicyRoute(fwmark uint32, tableID int, gw net.IP, dev string) error {
	// Add ip rule: fwmark → lookup table
	rule := netlink.NewRule()
	rule.Mark = fwmark
	mask := uint32(0xFFFFFFFF)
	rule.Mask = &mask
	rule.Table = tableID
	rule.Priority = 100 + tableID

	if err := netlink.RuleAdd(rule); err != nil {
		return fmt.Errorf("add policy rule for fwmark 0x%x table %d: %w", fwmark, tableID, err)
	}

	// Find the device
	link, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("find device %s for policy route: %w", dev, err)
	}

	// Add default route in the table
	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Gw:        gw,
		Table:     tableID,
		Dst:       nil, // default route
	}

	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("add policy route default via %s dev %s table %d: %w", gw, dev, tableID, err)
	}

	return nil
}

// TeardownPolicyRoute removes the policy routing rule and route table.
func TeardownPolicyRoute(fwmark uint32, tableID int) error {
	// Remove ip rule
	rule := netlink.NewRule()
	rule.Mark = fwmark
	mask := uint32(0xFFFFFFFF)
	rule.Mask = &mask
	rule.Table = tableID
	rule.Priority = 100 + tableID

	if err := netlink.RuleDel(rule); err != nil {
		return fmt.Errorf("del policy rule for fwmark 0x%x table %d: %w", fwmark, tableID, err)
	}

	// Flush all routes in the table
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: tableID}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return fmt.Errorf("list routes in table %d: %w", tableID, err)
	}

	for _, route := range routes {
		if err := netlink.RouteDel(&route); err != nil {
			return fmt.Errorf("del route in table %d: %w", tableID, err)
		}
	}

	return nil
}

// SetupFwmarkInNftables adds a nftables rule to mark packets from a veth
// with the given fwmark for policy routing to egress gateways.
// The mark is set in prerouting (mangle priority) so it takes effect
// BEFORE the routing decision — required for forwarded traffic to be
// rerouted via policy rules.
func SetupFwmarkInNftables(hf *HostFirewall, vethName string, fwmark uint32) error {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	// Create or reuse a mangle-priority prerouting chain for marking.
	// Prerouting runs before the routing decision, so fwmark-based
	// policy routing works for forwarded traffic.
	mangleChain := hf.conn.AddChain(&nftables.Chain{
		Name:     "mangle_prerouting",
		Table:    hf.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityMangle,
	})

	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: mangleChain,
		Exprs: []expr.Any{
			// Match iifname == vethName
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: ifnameBytes(vethName)},
			// Set fwmark
			&expr.Immediate{Register: 1, Data: bitmask32(fwmark)},
			&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
		},
	})

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush fwmark rule: %w", err)
	}

	return nil
}

// RemoveFwmarkInNftables is a placeholder — in production, we'd track and remove
// per-veth mark rules. For the PoC, the table teardown in Close() cleans everything.
func RemoveFwmarkInNftables(_ *HostFirewall, _ string) error {
	return nil
}
