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
//
// The rule handle is tracked so it can be surgically removed later.
func SetupFwmarkInNftables(hf *HostFirewall, vethName string, fwmark uint32) error {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	// Remove existing rule for this veth if present (idempotent reassign)
	if handle, ok := hf.fwmarkRules[vethName]; ok {
		hf.conn.DelRule(&nftables.Rule{
			Table:  hf.table,
			Chain:  hf.mangleChain,
			Handle: handle,
		})
		delete(hf.fwmarkRules, vethName)
	}

	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: hf.mangleChain,
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

	// Retrieve the rule handle for later removal.
	// The rule we just added is the last one in the chain for this veth.
	rules, err := hf.conn.GetRules(hf.table, hf.mangleChain)
	if err != nil {
		return fmt.Errorf("get mangle rules to track handle: %w", err)
	}
	for _, r := range rules {
		if matchesFwmarkRule(r, vethName) {
			hf.fwmarkRules[vethName] = r.Handle
			break
		}
	}

	return nil
}

// matchesFwmarkRule checks if a rule is the fwmark rule for the given veth
// by inspecting its expressions for an iifname match.
func matchesFwmarkRule(r *nftables.Rule, vethName string) bool {
	expected := ifnameBytes(vethName)
	for i, e := range r.Exprs {
		cmp, ok := e.(*expr.Cmp)
		if !ok || i == 0 {
			continue
		}
		// Check if previous expr is MetaKeyIIFNAME and this Cmp matches our veth
		if meta, ok := r.Exprs[i-1].(*expr.Meta); ok && meta.Key == expr.MetaKeyIIFNAME {
			if len(cmp.Data) == len(expected) {
				match := true
				for j := range cmp.Data {
					if cmp.Data[j] != expected[j] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false
}

// RemoveFwmarkInNftables removes the per-veth fwmark rule by its tracked handle.
// Idempotent: returns nil if no rule exists for the veth.
func RemoveFwmarkInNftables(hf *HostFirewall, vethName string) error {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	handle, ok := hf.fwmarkRules[vethName]
	if !ok {
		return nil // already removed or never set
	}

	hf.conn.DelRule(&nftables.Rule{
		Table:  hf.table,
		Chain:  hf.mangleChain,
		Handle: handle,
	})

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush remove fwmark rule for %s: %w", vethName, err)
	}

	delete(hf.fwmarkRules, vethName)
	return nil
}
