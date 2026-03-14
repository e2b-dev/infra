package v2

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// migrationDNATChainName is the nftables chain name for migration DNAT rules.
const migrationDNATChainName = "migration_dnat"

// migrationForwardChainName is the nftables chain name for migration forward rules.
const migrationForwardChainName = "migration_forward"

// migrationDNATEntry tracks a pair of nftables rules (DNAT + forward) for one migrated IP.
type migrationDNATEntry struct {
	oldHostIP  net.IP
	newHostIP  net.IP
	wgDevice   string
	dnatHandle uint64 // nftables rule handle in migration_dnat chain
	fwdHandle  uint64 // nftables rule handle in migration_forward chain
}

// SetupIPForward routes traffic destined for oldHostIP through WireGuard to the
// target node. This is called on the SOURCE node after migration so that clients
// using the old host IP are forwarded through WireGuard to the target.
//
// Equivalent to:
//
//	ip route add <oldHostIP>/32 via <targetWgIP> dev <wgDevice>
func SetupIPForward(oldHostIP, targetWgIP net.IP, wgDevice string) error {
	link, err := netlink.LinkByName(wgDevice)
	if err != nil {
		return fmt.Errorf("find WireGuard device %s: %w", wgDevice, err)
	}

	route := &netlink.Route{
		Dst: &net.IPNet{
			IP:   oldHostIP.To4(),
			Mask: net.CIDRMask(32, 32),
		},
		Gw:        targetWgIP.To4(),
		LinkIndex: link.Attrs().Index,
	}

	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("add migration route %s/32 via %s dev %s: %w",
			oldHostIP, targetWgIP, wgDevice, err)
	}

	return nil
}

// TeardownIPForward removes the forwarding route for a migrated IP.
// Called on the SOURCE node when migration forwarding is no longer needed.
func TeardownIPForward(oldHostIP net.IP, wgDevice string) error {
	link, err := netlink.LinkByName(wgDevice)
	if err != nil {
		return fmt.Errorf("find WireGuard device %s: %w", wgDevice, err)
	}

	route := &netlink.Route{
		Dst: &net.IPNet{
			IP:   oldHostIP.To4(),
			Mask: net.CIDRMask(32, 32),
		},
		LinkIndex: link.Attrs().Index,
	}

	if err := netlink.RouteDel(route); err != nil {
		return fmt.Errorf("del migration route %s/32 dev %s: %w",
			oldHostIP, wgDevice, err)
	}

	return nil
}

// SetupMigrationDNAT adds a DNAT rule on the TARGET node so that traffic arriving
// via WireGuard for the old host IP gets redirected to the new slot's host IP.
//
// This creates (or reuses) a "migration_dnat" chain in the host firewall table:
//
//	chain migration_dnat { type nat hook prerouting priority -90; }
//	ip daddr <oldHostIP> dnat to <newHostIP>
//
// It also adds a forward rule to allow WireGuard response traffic to reach the new veth:
//
//	chain migration_forward { type filter hook forward priority -10; }
//	iifname "wg0" ip daddr <newHostIP> ct state established,related accept
//
// Rule handles are tracked per-HostFirewall for surgical per-IP removal.
// Calling this again with the same oldHostIP replaces the existing rules.
func SetupMigrationDNAT(hf *HostFirewall, oldHostIP, newHostIP net.IP, wgDevice string) error {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	oldIP4 := oldHostIP.To4()
	newIP4 := newHostIP.To4()
	if oldIP4 == nil || newIP4 == nil {
		return fmt.Errorf("invalid IPs: old=%v new=%v", oldHostIP, newHostIP)
	}

	key := oldHostIP.String()

	// Idempotency: if rules already exist for this IP, remove them first.
	if existing, ok := hf.migrationRules[key]; ok {
		if err := hf.deleteMigrationRulesLocked(existing); err != nil {
			return fmt.Errorf("replace existing migration rules for %s: %w", key, err)
		}
		delete(hf.migrationRules, key)
	}

	// Create or reuse migration DNAT prerouting chain (slightly higher priority
	// than the main prerouting chain at -100, so it runs first).
	dnatChain := hf.conn.AddChain(&nftables.Chain{
		Name:     migrationDNATChainName,
		Table:    hf.table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(-90),
	})

	// ip daddr <oldHostIP> dnat to <newHostIP>
	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: dnatChain,
		Exprs: []expr.Any{
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       16, // dst IP offset
				Len:          4,
			},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     oldIP4,
			},
			&expr.Immediate{
				Register: 1,
				Data:     newIP4,
			},
			&expr.NAT{
				Type:       expr.NATTypeDestNAT,
				Family:     unix.NFPROTO_IPV4,
				RegAddrMin: 1,
				RegAddrMax: 1,
			},
		},
	})

	// Forward chain: allow WireGuard traffic that was DNAT'd to reach the veth.
	//
	// SECURITY NOTE (PoC limitation): This rule allows all connection states
	// (including NEW) from wg0 to the migrated host IP. This bypasses the TCP
	// firewall proxy because traffic arrives on wg0, not a v2 veth. Acceptable
	// for the PoC because:
	//   1. Only traffic for the specific migrated IP is affected (not all wg0 traffic)
	//   2. The rule only exists while migration forwarding is active
	//   3. Production replaces IP forwarding with edge/egress service cutover
	//      which routes through the normal proxy path (design §7.3 step 11)
	fwdChain := hf.conn.AddChain(&nftables.Chain{
		Name:     migrationForwardChainName,
		Table:    hf.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityRef(-10), // before main forward (0)
	})

	// iifname "wg0" ip daddr <newHostIP> accept
	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: fwdChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     ifnameBytes(wgDevice),
			},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       16,
				Len:          4,
			},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     newIP4,
			},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	})

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush migration DNAT rules: %w", err)
	}

	// After flush, retrieve rule handles from the kernel for later surgical removal.
	dnatHandle, err := findRuleHandleByDstPayload(hf.conn, hf.table, dnatChain, oldIP4)
	if err != nil {
		return fmt.Errorf("find DNAT rule handle: %w", err)
	}

	fwdHandle, err := findRuleHandleByDstPayload(hf.conn, hf.table, fwdChain, newIP4)
	if err != nil {
		return fmt.Errorf("find forward rule handle: %w", err)
	}

	hf.migrationRules[key] = &migrationDNATEntry{
		oldHostIP:  oldIP4,
		newHostIP:  newIP4,
		wgDevice:   wgDevice,
		dnatHandle: dnatHandle,
		fwdHandle:  fwdHandle,
	}

	return nil
}

// TeardownMigrationDNAT removes the DNAT and forward rules for a specific migrated IP.
// Called on the TARGET node when migration forwarding is no longer needed.
//
// Uses tracked rule handles for surgical per-rule removal. When the last migration
// rule is removed, the chains are deleted entirely.
func TeardownMigrationDNAT(hf *HostFirewall, oldHostIP net.IP) error {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	key := oldHostIP.String()
	entry, ok := hf.migrationRules[key]
	if !ok {
		return fmt.Errorf("no migration DNAT entry for %s", key)
	}

	if err := hf.deleteMigrationRulesLocked(entry); err != nil {
		return err
	}

	// Only update map after successful flush — on failure the rules may still
	// be present and the caller can retry.
	delete(hf.migrationRules, key)

	// If no more migration rules, remove the chains entirely.
	if len(hf.migrationRules) == 0 {
		hf.conn.DelChain(&nftables.Chain{
			Name:  migrationDNATChainName,
			Table: hf.table,
		})
		hf.conn.DelChain(&nftables.Chain{
			Name:  migrationForwardChainName,
			Table: hf.table,
		})

		if err := hf.conn.Flush(); err != nil {
			return fmt.Errorf("flush migration chain cleanup: %w", err)
		}
	}

	return nil
}

// deleteMigrationRulesLocked deletes the DNAT and forward rules for an entry.
// Caller must hold hf.mu.
func (hf *HostFirewall) deleteMigrationRulesLocked(entry *migrationDNATEntry) error {
	if entry.dnatHandle != 0 {
		if err := hf.conn.DelRule(&nftables.Rule{
			Table: hf.table,
			Chain: &nftables.Chain{
				Name:  migrationDNATChainName,
				Table: hf.table,
			},
			Handle: entry.dnatHandle,
		}); err != nil {
			return fmt.Errorf("del DNAT rule handle %d: %w", entry.dnatHandle, err)
		}
	}

	if entry.fwdHandle != 0 {
		if err := hf.conn.DelRule(&nftables.Rule{
			Table: hf.table,
			Chain: &nftables.Chain{
				Name:  migrationForwardChainName,
				Table: hf.table,
			},
			Handle: entry.fwdHandle,
		}); err != nil {
			return fmt.Errorf("del forward rule handle %d: %w", entry.fwdHandle, err)
		}
	}

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush migration rule deletion: %w", err)
	}

	return nil
}

// findRuleHandleByDstPayload searches a chain's rules for one containing a
// Payload(offset=16, len=4) immediately followed by Cmp(eq, ip) — the standard
// pattern for matching IPv4 destination address. Returns the rule's kernel handle.
//
// This is more precise than matching any Cmp expression, because it won't
// false-match on ifname comparisons or other fields.
func findRuleHandleByDstPayload(conn *nftables.Conn, table *nftables.Table, chain *nftables.Chain, dstIP net.IP) (uint64, error) {
	rules, err := conn.GetRules(table, chain)
	if err != nil {
		return 0, fmt.Errorf("get rules: %w", err)
	}

	ip4 := dstIP.To4()
	for _, r := range rules {
		exprs := r.Exprs
		for i := 0; i+1 < len(exprs); i++ {
			payload, ok := exprs[i].(*expr.Payload)
			if !ok {
				continue
			}
			// IPv4 dst address: network header, offset 16, length 4
			if payload.Base != expr.PayloadBaseNetworkHeader || payload.Offset != 16 || payload.Len != 4 {
				continue
			}
			cmp, ok := exprs[i+1].(*expr.Cmp)
			if !ok {
				continue
			}
			if cmp.Op == expr.CmpOpEq && len(cmp.Data) == 4 &&
				cmp.Data[0] == ip4[0] && cmp.Data[1] == ip4[1] &&
				cmp.Data[2] == ip4[2] && cmp.Data[3] == ip4[3] {
				return r.Handle, nil
			}
		}
	}

	return 0, fmt.Errorf("no rule matching dst IP %s found in chain %s", dstIP, chain.Name)
}
