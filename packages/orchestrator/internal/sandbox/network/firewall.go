package network

import (
	"fmt"
	"net/netip"

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

	predefinedDenySet  set.Set
	predefinedAllowSet set.Set

	userDenySet  set.Set
	userAllowSet set.Set

	tapInterface string

	allowedRanges []string
}

func NewFirewall(tapIf string, hyperloopIP string) (*Firewall, error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("new nftables conn: %w", err)
	}

	table := conn.AddTable(&nftables.Table{
		Name:   tableName,
		Family: nftables.TableFamilyINet,
	})

	// Filter chain in PREROUTING
	// This handles: allow/deny decisions for traffic from the tap interface.
	// Default policy is DROP: only explicitly allowed traffic passes through.
	policy := nftables.ChainPolicyDrop
	filterChain := conn.AddChain(&nftables.Chain{
		Name:     "PREROUTE_FILTER",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(-150),
		Policy:   &policy,
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
		allowedRanges:      []string{fmt.Sprintf("%s/32", hyperloopIP)},
		filterChain:        filterChain,
	}

	// Add firewall rules to the chain
	if err := fw.installRules(); err != nil {
		return nil, err
	}

	// Populate the sets with initial data
	err = fw.Reset()
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

// addNonTCPSetFilterRule adds a filter rule that matches ONLY non-TCP traffic to destinations in a set.
// If drop is true, packets are dropped. Otherwise, they are accepted.
// TCP traffic is NOT affected by this rule (iptables REDIRECT handles TCP traffic).
func (fw *Firewall) addNonTCPSetFilterRule(ipSet *nftables.Set, drop bool) {
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
			// Match non-TCP protocol (protocol != TCP)
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpNeq,
				Register: 1,
				Data:     []byte{unix.IPPROTO_TCP},
			},
			// Check dest in set
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(ipSet, 1)),
			verdict...,
		),
	})
}

func (fw *Firewall) installRules() error {
	// ============================================================
	// FILTER CHAIN (PREROUTING, priority -150)
	// Default policy: DROP (deny by default)
	// Order:
	//   1. ESTABLISHED/RELATED → accept (allow responses even from denied ranges)
	//   2. predefinedAllowSet → accept (all protocols)
	//   3. predefinedDenySet → DROP (all protocols, hard block)
	//   4. Non-TCP: userAllowSet → accept
	//   5. Non-TCP: userDenySet → DROP
	//   6. TCP from tap → accept (iptables REDIRECT handles routing to proxy)
	//   7. Non-tap traffic → accept (host traffic should not be filtered)
	//   8. Default: DROP (non-TCP from tap not in any set is dropped)
	//
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

	// Rule 4: Non-TCP + userAllowSet → accept
	// Only non-TCP traffic is affected; TCP goes to proxy
	fw.addNonTCPSetFilterRule(fw.userAllowSet.Set(), false)

	// Rule 5: Non-TCP + userDenySet → DROP
	// Only non-TCP traffic is affected; TCP goes to proxy
	fw.addNonTCPSetFilterRule(fw.userDenySet.Set(), true)

	// Rule 6: Accept all TCP from tap interface → let iptables REDIRECT handle it
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(append(fw.tapIfaceMatch(),
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     []byte{unix.IPPROTO_TCP},
			}),
			accept()...,
		),
	})

	// Rule 7: Accept traffic NOT from the tap interface (host traffic should not be filtered)
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(
			[]expr.Any{
				&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
				&expr.Cmp{
					Register: 1,
					Op:       expr.CmpOpNeq,
					Data:     append([]byte(fw.tapInterface), 0),
				},
			},
			accept()...,
		),
	})

	// Default policy: DROP
	// - Non-TCP from tap not in any set: dropped (prevents unfiltered UDP/ICMP egress)
	// - TCP from tap: accepted above, iptables REDIRECT handles routing to proxy
	// - Non-tap traffic: accepted above (host traffic unaffected)

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush nftables changes: %w", err)
	}

	return nil
}

// AddDeniedCIDR adds a single CIDR to the deny set at runtime.
func (fw *Firewall) AddDeniedCIDR(cidr string) error {
	err := addCIDRToSet(fw.conn, fw.userDenySet, cidr)
	if err != nil {
		return fmt.Errorf("add denied CIDR to set: %w", err)
	}

	err = fw.conn.Flush()
	if err != nil {
		return fmt.Errorf("flush add denied changes: %w", err)
	}

	return nil
}

// AddAllowedCIDR adds a single CIDR to the allow set at runtime.
func (fw *Firewall) AddAllowedCIDR(cidr string) error {
	err := addCIDRToSet(fw.conn, fw.userAllowSet, cidr)
	if err != nil {
		return fmt.Errorf("add allowed CIDR to set: %w", err)
	}

	err = fw.conn.Flush()
	if err != nil {
		return fmt.Errorf("flush add allowed changes: %w", err)
	}

	return nil
}

func (fw *Firewall) Reset() error {
	if err := fw.ResetDeniedSets(); err != nil {
		return fmt.Errorf("clear denied set: %w", err)
	}
	if err := fw.ResetAllowedSets(); err != nil {
		return fmt.Errorf("clear allow set: %w", err)
	}

	return nil
}

// ResetDeniedSets resets the deny set back to original ranges.
func (fw *Firewall) ResetDeniedSets() error {
	// Always deny the default ranges
	if err := fw.predefinedDenySet.ClearAndAddElements(fw.conn, sandbox_network.DeniedSandboxSetData); err != nil {
		return err
	}

	// User defined denied ranges
	if err := fw.userDenySet.ClearAndAddElements(fw.conn, nil); err != nil {
		return err
	}

	return fw.conn.Flush()
}

// ResetAllowedSets resets allow set back to original ranges.
func (fw *Firewall) ResetAllowedSets() error {
	// Always allowed ranges
	initData, err := set.AddressStringsToSetData(fw.allowedRanges)
	if err != nil {
		return fmt.Errorf("parse initial allowed CIDRs: %w", err)
	}
	if err := fw.predefinedAllowSet.ClearAndAddElements(fw.conn, initData); err != nil {
		return err
	}

	// User defined allowed ranges
	if err := fw.userAllowSet.ClearAndAddElements(fw.conn, nil); err != nil {
		return err
	}

	return fw.conn.Flush()
}

func addCIDRToSet(conn *nftables.Conn, ipset set.Set, cidr string) error {
	current, err := ipset.Elements(conn)
	if err != nil {
		return err
	}

	// The checked range is 0.0.0.0 to 255.255.255.254, because when 255.255.255.255 is added, it's then requested as 255.255.255.254.
	if len(current) == 1 && current[0].AddressRangeStart == netip.MustParseAddr("0.0.0.0") && current[0].AddressRangeEnd == netip.MustParseAddr("255.255.255.254") {
		// Because 0.0.0.0/0 is not valid IP per GoLang, we can't add new addresses to the set.
		return nil
	}

	// 0.0.0.0/0 is not valid IP per GoLang, so we handle it as a special case
	if cidr == sandbox_network.AllInternetTrafficCIDR {
		conn.FlushSet(ipset.Set())

		toAppend := []nftables.SetElement{
			{
				Key: netip.MustParseAddr("0.0.0.0").AsSlice(),
			},
			{
				Key:         netip.MustParseAddr("255.255.255.255").AsSlice(),
				IntervalEnd: true,
			},
		}

		if err := conn.SetAddElements(ipset.Set(), toAppend); err != nil {
			return fmt.Errorf("add elements to denied set: %w", err)
		}

		return nil
	}

	data, err := set.AddressStringsToSetData([]string{cidr})
	if err != nil {
		return err
	}

	merged := append(current, data...)

	return ipset.ClearAndAddElements(conn, merged)
}
