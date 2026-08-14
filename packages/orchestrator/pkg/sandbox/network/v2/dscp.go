//go:build linux

package v2

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

const (
	maxDSCP = 63 // DSCP is the top 6 bits of the IPv4 TOS byte.

	// nsMangleChainName is the postrouting mangle chain in the in-namespace
	// "slot-firewall" table, holding the egress DSCP rule — the same hook
	// v1's iptables mangle/POSTROUTING rule uses, so the TOS byte on the
	// veth link is identical between versions.
	nsMangleChainName = "postroute_mangle"
)

// SetupEgressDSCP creates the in-namespace postrouting mangle chain and, for
// a non-zero value, the rule stamping DSCP on everything leaving the sandbox
// netns through the vpeer uplink. Must run inside the namespace, on the conn
// SetupNamespaceNAT uses.
func SetupEgressDSCP(conn *nftables.Conn, table *nftables.Table, vpeerIface string, dscp uint8) error {
	if dscp > maxDSCP {
		return fmt.Errorf("egress DSCP %d out of range (0..%d)", dscp, maxDSCP)
	}

	chain := conn.AddChain(&nftables.Chain{
		Name:     nsMangleChainName,
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityMangle,
	})

	if dscp > 0 {
		conn.AddRule(&nftables.Rule{
			Table: table,
			Chain: chain,
			Exprs: dscpSetExprs(vpeerIface, dscp),
		})
	}

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("flush egress DSCP setup: %w", err)
	}

	return nil
}

// ApplyEgressDSCP re-stamps the slot's egress rule so a build can be marked
// differently from a regular sandbox, mirroring v1's semantics: 0 removes the
// rule, a matching installed value is a no-op, and the add and delete land in
// one atomic batch so egress is never unmarked during a restamp and a failed
// apply keeps the previous class installed. The kernel ruleset is the source
// of truth for the currently stamped value.
func ApplyEgressDSCP(slot *network.Slot, dscp uint8) error {
	if dscp > maxDSCP {
		return fmt.Errorf("egress DSCP %d out of range (0..%d)", dscp, maxDSCP)
	}

	nsHandle, err := ns.GetNS(filepath.Join(network.NetNamespacesDir, slot.NamespaceID()))
	if err != nil {
		return fmt.Errorf("get slot network namespace %q: %w", slot.NamespaceID(), err)
	}
	defer nsHandle.Close()

	return nsHandle.Do(func(_ ns.NetNS) error {
		conn, err := nftables.New(nftables.AsLasting())
		if err != nil {
			return fmt.Errorf("nftables conn in namespace: %w", err)
		}
		defer conn.CloseLasting()

		table := &nftables.Table{Name: "slot-firewall", Family: nftables.TableFamilyINet}
		chain := &nftables.Chain{Name: nsMangleChainName, Table: table}

		current, currentDSCP, err := findDSCPRule(conn, table, chain, slot.VpeerName())
		if err != nil {
			return fmt.Errorf("find DSCP rule for %s: %w", slot.NamespaceID(), err)
		}
		if current == nil && dscp == 0 {
			return nil
		}
		if current != nil && currentDSCP == dscp {
			return nil
		}

		if dscp > 0 {
			conn.AddRule(&nftables.Rule{
				Table: table,
				Chain: chain,
				Exprs: dscpSetExprs(slot.VpeerName(), dscp),
			})
		}
		if current != nil {
			conn.DelRule(&nftables.Rule{
				Table:  table,
				Chain:  chain,
				Handle: current.Handle,
			})
		}

		if err := conn.Flush(); err != nil {
			return fmt.Errorf("flush DSCP %d rule for %s: %w", dscp, slot.NamespaceID(), err)
		}

		return nil
	})
}

// findDSCPRule returns the vpeer's DSCP rule and its stamped value, or nil
// when none is installed.
func findDSCPRule(conn *nftables.Conn, table *nftables.Table, chain *nftables.Chain, vpeerIface string) (*nftables.Rule, uint8, error) {
	rules, err := conn.GetRules(table, chain)
	if err != nil {
		return nil, 0, fmt.Errorf("get mangle rules: %w", err)
	}

	for _, r := range rules {
		if dscp, ok := dscpRuleValue(r, vpeerIface); ok {
			return r, dscp, nil
		}
	}

	return nil, 0, nil
}

// dscpRuleValue reports whether r is the DSCP rule for the given uplink — an
// oifname match plus a payload write — and decodes the DSCP it stamps.
func dscpRuleValue(r *nftables.Rule, vpeerIface string) (uint8, bool) {
	var tos uint8
	writes := false

	for _, e := range r.Exprs {
		switch v := e.(type) {
		case *expr.Payload:
			if v.OperationType == expr.PayloadWrite {
				writes = true
			}
		case *expr.Bitwise:
			// The xor carries the new TOS byte in the second header byte.
			if len(v.Xor) == 2 {
				tos = v.Xor[1]
			}
		}
	}

	if !writes || !hasOifnameCmp(r, vpeerIface) {
		return 0, false
	}

	return tos >> 2, true
}

// hasOifnameCmp reports whether the rule compares oifname against the given
// interface name.
func hasOifnameCmp(r *nftables.Rule, name string) bool {
	expected := ifnameBytes(name)
	for i, e := range r.Exprs {
		if i == 0 {
			continue
		}
		cmp, ok := e.(*expr.Cmp)
		if !ok {
			continue
		}
		if meta, ok := r.Exprs[i-1].(*expr.Meta); ok && meta.Key == expr.MetaKeyOIFNAME && bytes.Equal(cmp.Data, expected) {
			return true
		}
	}

	return false
}

// dscpSetExprs builds: meta nfproto ipv4 oifname <vpeer> ip dscp set <dscp> —
// a write of the TOS byte that preserves the ECN bits and fixes the IPv4
// header checksum, equivalent to v1's `-o <vpeer> -j DSCP --set-dscp <dscp>`.
//
// The read-modify-write spans the two leading header bytes rather than the TOS
// byte alone: the kernel's inet checksum fixup works on 16-bit words, so a
// 1-byte write at odd offset 1 folds the delta into the wrong half and every
// stamped packet leaves with a corrupt header checksum. Masking version/IHL
// through keeps them unchanged.
func dscpSetExprs(vpeerIface string, dscp uint8) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: []byte{unix.NFPROTO_IPV4}},
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: ifnameBytes(vpeerIface)},
		&expr.Payload{
			OperationType: expr.PayloadLoad,
			DestRegister:  1,
			Base:          expr.PayloadBaseNetworkHeader,
			Offset:        0, // version/IHL + TOS
			Len:           2,
		},
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            2,
			Mask:           []byte{0xff, 0x03}, // keep version/IHL and the ECN bits
			Xor:            []byte{0x00, dscp << 2},
		},
		&expr.Payload{
			OperationType:  expr.PayloadWrite,
			SourceRegister: 1,
			Base:           expr.PayloadBaseNetworkHeader,
			Offset:         0,
			Len:            2,
			CsumType:       expr.CsumTypeInet,
			CsumOffset:     10, // IPv4 header checksum
		},
	}
}
