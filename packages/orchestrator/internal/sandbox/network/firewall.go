package network

import (
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/ngrok/firewall_toolkit/pkg/expressions"
	"github.com/ngrok/firewall_toolkit/pkg/set"
	"golang.org/x/sys/unix"

	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

const tableName = "slot-firewall"

type Firewall struct {
	conn  *nftables.Conn
	table *nftables.Table

	// Filter chain in PREROUTING
	filterChain *nftables.Chain

	// userNonTCPChain is a regular chain (no hook) used for user allow/deny
	// rules on non-TCP traffic. It is flushed and rebuilt on each
	// ReplaceUserRules call to support dynamic port-specific rules.
	userNonTCPChain *nftables.Chain

	predefinedDenySet  set.Set
	predefinedAllowSet set.Set

	userDenySet  set.Set
	userAllowSet set.Set

	tapInterface string

	allowedRanges []string
}

func NewFirewall(tapIf string, orchestratorInternalIP string) (*Firewall, error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("new nftables conn: %w", err)
	}

	table := conn.AddTable(&nftables.Table{
		Name:   tableName,
		Family: nftables.TableFamilyINet,
	})

	// Filter chain in PREROUTING
	// This handles: allow/deny decisions for traffic from the tap interface
	policy := nftables.ChainPolicyAccept
	filterChain := conn.AddChain(&nftables.Chain{
		Name:     "PREROUTE_FILTER",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(-150),
		Policy:   &policy,
	})

	// User non-TCP chain — regular chain (no hook), entered via jump from filterChain.
	// Contains user allow/deny rules for non-TCP traffic, flushed and rebuilt
	// on each ReplaceUserRules call.
	userNonTCPChain := conn.AddChain(&nftables.Chain{
		Name:  "USER_NONTCP_RULES",
		Table: table,
	})

	// Create deny-set and allow-set
	alwaysDenySet, err := set.New(conn, table, "filtered_always_denylist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new deny set: %w", err)
	}
	alwaysAllowSet, err := set.New(conn, table, "filtered_always_allowlist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new allow set: %w", err)
	}

	denySet, err := set.New(conn, table, "filtered_denylist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new deny set: %w", err)
	}
	allowSet, err := set.New(conn, table, "filtered_allowlist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new allow set: %w", err)
	}

	fw := &Firewall{
		conn:               conn,
		table:              table,
		predefinedDenySet:  alwaysDenySet,
		predefinedAllowSet: alwaysAllowSet,
		userDenySet:        denySet,
		userAllowSet:       allowSet,
		tapInterface:       tapIf,
		allowedRanges:      []string{fmt.Sprintf("%s/32", orchestratorInternalIP)},
		filterChain:        filterChain,
		userNonTCPChain:    userNonTCPChain,
	}

	// Add firewall rules to the chain
	if err := fw.installRules(); err != nil {
		return nil, err
	}

	// Populate the sets with initial data
	err = fw.ReplaceUserRules(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error while configuring initial data: %w", err)
	}

	return fw, nil
}

func (fw *Firewall) Close() error {
	return fw.conn.CloseLasting()
}

// tapIfaceMatch returns expressions that match packets from the tap interface.
func (fw *Firewall) tapIfaceMatch() []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{
			Register: 1,
			Op:       expr.CmpOpEq,
			Data:     append([]byte(fw.tapInterface), 0), // null-terminated interface name
		},
	}
}

// accept returns an expression that accepts the packet.
func accept() []expr.Any {
	return []expr.Any{
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// addSetFilterRule adds a filter rule that matches destination IPs in a set.
// If drop is true, packets are dropped. Otherwise, they are accepted.
// This applies to ALL protocols.
func (fw *Firewall) addSetFilterRule(ipSet *nftables.Set, drop bool) {
	var verdict []expr.Any
	if drop {
		verdict = []expr.Any{&expr.Verdict{Kind: expr.VerdictDrop}}
	} else {
		verdict = accept()
	}

	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(append(fw.tapIfaceMatch(),
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(ipSet, 1)),
			verdict...,
		),
	})
}

func (fw *Firewall) installRules() error {
	// ============================================================
	// FILTER CHAIN (PREROUTING, priority -150)
	// Order:
	//   1. ESTABLISHED/RELATED → accept (allow responses even from denied ranges)
	//   2. predefinedAllowSet → accept (all protocols)
	//   3. predefinedDenySet → DROP (all protocols, hard block)
	//   4. Non-TCP from tap → jump to USER_NONTCP_RULES chain
	//   5. Default: ACCEPT (TCP handled by iptables REDIRECT)
	//
	// USER_NONTCP_RULES chain (populated by ReplaceUserRules):
	//   1. userAllowSet → accept (all-ports entries)
	//   2. Port-specific allow rules → accept
	//   3. userDenySet → DROP (all-ports entries)
	//   4. Port-specific deny rules → DROP
	//   5. Implicit return → parent chain default ACCEPT
	// ============================================================

	// Rule 1: Allow ESTABLISHED/RELATED connections - all protocols
	// This ensures response packets are allowed even if the source is in predefinedDenySet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(append(fw.tapIfaceMatch(),
			// Load CT state
			&expr.Ct{Key: expr.CtKeySTATE, Register: 1},
			// Check ESTABLISHED or RELATED
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
				Xor:            binaryutil.NativeEndian.PutUint32(0),
			},
			&expr.Cmp{
				Op:       expr.CmpOpNeq,
				Register: 1,
				Data:     binaryutil.NativeEndian.PutUint32(0),
			}),
			accept()...,
		),
	})

	// Rule 2: predefinedAllowSet → accept (all protocols)
	fw.addSetFilterRule(fw.predefinedAllowSet.Set(), false)

	// Rule 3: predefinedDenySet → DROP (all protocols, hard block)
	fw.addSetFilterRule(fw.predefinedDenySet.Set(), true)

	// Rule 4: Non-TCP traffic from tap → jump to user rules chain
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(fw.tapIfaceMatch(),
			// Match non-TCP protocol (protocol != TCP)
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpNeq,
				Register: 1,
				Data:     []byte{unix.IPPROTO_TCP},
			},
			&expr.Verdict{Kind: expr.VerdictJump, Chain: fw.userNonTCPChain.Name},
		),
	})

	// Default policy: ACCEPT
	// - Non-TCP returning from user chain: allowed (default policy)
	// - TCP: iptables REDIRECT handles TCP traffic to proxy

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush nftables changes: %w", err)
	}

	return nil
}

// ReplaceUserRules atomically replaces all user firewall rules.
// Entries may include optional port ranges (e.g. "8.8.8.8:53", "10.0.0.0/8:1-1024").
// All-ports entries use IP set matching (fast path). Port-specific entries are
// added as individual nftables rules matching destination ports.
func (fw *Firewall) ReplaceUserRules(allowedEntries, deniedEntries []string) error {
	// Parse raw entry strings into rules.
	allowedRules, err := sandbox_network.ParseRules(allowedEntries)
	if err != nil {
		return fmt.Errorf("parse allowed rules: %w", err)
	}
	deniedRules, err := sandbox_network.ParseRules(deniedEntries)
	if err != nil {
		return fmt.Errorf("parse denied rules: %w", err)
	}

	// Separate all-ports CIDRs (for IP set fast path) from port-specific rules.
	// Domain entries are skipped — they are only enforced by the TCP proxy.
	var allowedAllPorts []string
	var allowedSomePorts []sandbox_network.Rule
	for _, r := range allowedRules {
		if r.IsDomain {
			continue
		}
		if r.AllPorts() {
			allowedAllPorts = append(allowedAllPorts, sandbox_network.AddressStringToCIDR(r.Host))
		} else {
			allowedSomePorts = append(allowedSomePorts, r)
		}
	}

	var deniedAllPorts []string
	var deniedSomePorts []sandbox_network.Rule
	for _, r := range deniedRules {
		if r.IsDomain {
			continue
		}
		if r.AllPorts() {
			deniedAllPorts = append(deniedAllPorts, sandbox_network.AddressStringToCIDR(r.Host))
		} else {
			deniedSomePorts = append(deniedSomePorts, r)
		}
	}

	// 1. Reset predefined deny set to default blocked ranges (buffered, no flush).
	if err := fw.predefinedDenySet.ClearAndAddElements(fw.conn, sandbox_network.DeniedSandboxSetData); err != nil {
		return fmt.Errorf("reset predefined deny set: %w", err)
	}

	// 2. Reset predefined allow set to allowedRanges (buffered, no flush).
	allowedSetData, err := set.AddressStringsToSetData(fw.allowedRanges)
	if err != nil {
		return fmt.Errorf("parse initial allowed CIDRs: %w", err)
	}
	if err := fw.predefinedAllowSet.ClearAndAddElements(fw.conn, allowedSetData); err != nil {
		return fmt.Errorf("reset predefined allow set: %w", err)
	}

	// 3. Replace user deny set with all-ports denied CIDRs (buffered, no flush).
	if err := clearAndReplaceCIDRs(fw.conn, fw.userDenySet, deniedAllPorts); err != nil {
		return fmt.Errorf("replace user deny set: %w", err)
	}

	// 4. Replace user allow set with all-ports allowed CIDRs (buffered, no flush).
	if err := clearAndReplaceCIDRs(fw.conn, fw.userAllowSet, allowedAllPorts); err != nil {
		return fmt.Errorf("replace user allow set: %w", err)
	}

	// 5. Flush and rebuild user non-TCP chain.
	fw.conn.FlushChain(fw.userNonTCPChain)

	// All-ports allow (IP set lookup)
	fw.addUserChainSetRule(fw.userAllowSet.Set(), false)

	// Port-specific allow rules
	for _, r := range allowedSomePorts {
		if err := fw.addPortRule(r, false); err != nil {
			return fmt.Errorf("add port-specific allow rule for %q: %w", r.Host, err)
		}
	}

	// All-ports deny (IP set lookup)
	fw.addUserChainSetRule(fw.userDenySet.Set(), true)

	// Port-specific deny rules
	for _, r := range deniedSomePorts {
		if err := fw.addPortRule(r, true); err != nil {
			return fmt.Errorf("add port-specific deny rule for %q: %w", r.Host, err)
		}
	}

	// 6. Single atomic flush.
	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush atomic rule replacement: %w", err)
	}

	return nil
}

// addUserChainSetRule adds a rule to the user non-TCP chain that matches
// destination IPs in the given set. No tap/protocol checks are needed since
// the parent chain already filters for non-TCP traffic from the tap interface.
func (fw *Firewall) addUserChainSetRule(ipSet *nftables.Set, drop bool) {
	var verdict []expr.Any
	if drop {
		verdict = []expr.Any{&expr.Verdict{Kind: expr.VerdictDrop}}
	} else {
		verdict = accept()
	}

	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.userNonTCPChain,
		Exprs: append(
			[]expr.Any{
				expressions.IPv4DestinationAddress(1),
				expressions.IPSetLookUp(ipSet, 1),
			},
			verdict...,
		),
	})
}

// addPortRule adds a rule to the user non-TCP chain that matches non-TCP traffic
// to a specific destination IP/CIDR and port range. Works for any transport
// protocol with standard port layout (UDP, SCTP, DCCP, UDPLite). ICMP packets
// may technically match if their header bytes at the port offset fall in range,
// but this is harmless — ICMP is not a data channel.
func (fw *Firewall) addPortRule(rule sandbox_network.Rule, drop bool) error {
	host := sandbox_network.AddressStringToCIDR(rule.Host)
	_, ipNet, err := net.ParseCIDR(host)
	if err != nil {
		return fmt.Errorf("invalid CIDR %q: %w", host, err)
	}

	var verdict expr.VerdictKind
	if drop {
		verdict = expr.VerdictDrop
	} else {
		verdict = expr.VerdictAccept
	}

	exprs := []expr.Any{
		// Match destination IP/CIDR
		expressions.IPv4DestinationAddress(1),
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            4,
			Mask:           []byte(ipNet.Mask),
			Xor:            []byte{0, 0, 0, 0},
		},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ipNet.IP.To4()},

		// Load destination port (offset 2 in transport header — same for UDP, SCTP, DCCP)
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseTransportHeader,
			Offset:       2, // destination port offset in transport header
			Len:          2,
		},
	}

	// Match port range
	if rule.PortStart == rule.PortEnd {
		exprs = append(exprs, &expr.Cmp{
			Op:       expr.CmpOpEq,
			Register: 1,
			Data:     binaryutil.BigEndian.PutUint16(rule.PortStart),
		})
	} else {
		exprs = append(exprs, &expr.Range{
			Op:       expr.CmpOpEq,
			Register: 1,
			FromData: binaryutil.BigEndian.PutUint16(rule.PortStart),
			ToData:   binaryutil.BigEndian.PutUint16(rule.PortEnd),
		})
	}

	exprs = append(exprs, &expr.Verdict{Kind: verdict})

	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.userNonTCPChain,
		Exprs: exprs,
	})

	return nil
}

// clearAndReplaceCIDRs clears a set and repopulates it with the given CIDRs.
// All operations are buffered — nothing is sent to the kernel until conn.Flush().
// Handles the special 0.0.0.0/0 case which the firewall_toolkit validation
// rejects (0.0.0.0 is "unspecified") by directly creating nftables elements.
func clearAndReplaceCIDRs(conn *nftables.Conn, s set.Set, cidrs []string) error {
	if len(cidrs) == 0 {
		// Buffer a "clear set" command. Note: conn.FlushSet only appends to the
		// message buffer, it does NOT commit to the kernel. The actual kernel
		// commit happens in ReplaceUserRules via conn.Flush().
		conn.FlushSet(s.Set())

		return nil
	}

	// 0.0.0.0/0 must be handled specially: the firewall_toolkit's
	// ValidateAddress rejects 0.0.0.0 as "unspecified", so we bypass
	// the toolkit and create raw nftables interval elements directly.
	if slices.Contains(cidrs, sandbox_network.AllInternetTrafficCIDR) {
		conn.FlushSet(s.Set())

		elems := []nftables.SetElement{
			{Key: netip.MustParseAddr("0.0.0.0").AsSlice()},
			{Key: netip.MustParseAddr("255.255.255.255").AsSlice(), IntervalEnd: true},
		}
		if err := conn.SetAddElements(s.Set(), elems); err != nil {
			return fmt.Errorf("add all-traffic elements: %w", err)
		}

		return nil
	}

	// ClearAndAddElements buffers both a FlushSet and SetAddElements — no kernel
	// commit happens here, only when ReplaceUserRules calls conn.Flush().
	data, err := set.AddressStringsToSetData(cidrs)
	if err != nil {
		return err
	}

	return s.ClearAndAddElements(conn, data)
}
