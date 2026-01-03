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
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	tableName = "slot-firewall"

	// allowedMark is the mark value to signal "allowed" traffic (skip DNAT)
	allowedMark = 0x1

	defaultUseTCPFirewall = false
)

type Firewall struct {
	conn  *nftables.Conn
	table *nftables.Table

	// Filter chain in PREROUTING
	filterChain *nftables.Chain

	predefinedDenySet  set.Set
	predefinedAllowSet set.Set

	userDenySet  set.Set
	userAllowSet set.Set

	// useTCPFirewall controls whether TCP traffic is redirected to the proxy.
	// When false, all TCP packets are marked to skip the proxy redirect.
	useTCPFirewall bool
	// tcpFirewallSkipRule allows us to add/remove the rule dynamically by marking all TCP packets.
	tcpFirewallSkipRule *nftables.Rule

	filterInterface string

	allowedRanges []string
}

func NewFirewall(filterIf string, hyperloopIP string) (*Firewall, error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("new nftables conn: %w", err)
	}

	table := conn.AddTable(&nftables.Table{
		Name:   tableName,
		Family: nftables.TableFamilyINet,
	})

	// Filter chain in PREROUTING
	// This handles: allow/deny decisions and marking allowed traffic
	filterChain := conn.AddChain(&nftables.Chain{
		Name:     "PREROUTE_FILTER",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(-150),
		Policy:   utils.ToPtr(nftables.ChainPolicyAccept),
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
		filterInterface:    filterIf,
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

// interfaceMatch returns expressions that match packets from the filter interface.
func (fw *Firewall) interfaceMatch() []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{
			Register: 1,
			Op:       expr.CmpOpEq,
			Data:     append([]byte(fw.filterInterface), 0), // null-terminated interface name
		},
	}
}

// markAndAccept returns expressions that set the allowed mark and accept the packet.
func markAndAccept() []expr.Any {
	return []expr.Any{
		&expr.Immediate{
			Register: 1,
			Data:     binaryutil.NativeEndian.PutUint32(allowedMark),
		},
		&expr.Meta{
			Key:            expr.MetaKeyMARK,
			SourceRegister: true,
			Register:       1,
		},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// addSetFilterRule adds a filter rule that matches destination IPs in a set.
// If drop is true, packets are dropped. Otherwise, they are marked as allowed and accepted.
// This applies to ALL protocols.
func (fw *Firewall) addSetFilterRule(ipSet *nftables.Set, drop bool) {
	var verdict []expr.Any
	if drop {
		verdict = []expr.Any{&expr.Verdict{Kind: expr.VerdictDrop}}
	} else {
		verdict = markAndAccept()
	}

	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(append(fw.interfaceMatch(),
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(ipSet, 1)),
			verdict...,
		),
	})
}

// addNonTCPSetFilterRule adds a filter rule that matches ONLY non-TCP traffic to destinations in a set.
// If drop is true, packets are dropped. Otherwise, they are marked as allowed and accepted.
// TCP traffic is NOT affected by this rule (iptables REDIRECT handles proxy redirect).
func (fw *Firewall) addNonTCPSetFilterRule(ipSet *nftables.Set, drop bool) {
	var verdict []expr.Any
	if drop {
		verdict = []expr.Any{&expr.Verdict{Kind: expr.VerdictDrop}}
	} else {
		verdict = markAndAccept()
	}

	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(append(fw.interfaceMatch(),
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
	// Order:
	//   1. ESTABLISHED/RELATED → mark + accept (all protocols)
	//   2. predefinedAllowSet → mark + accept (all protocols)
	//   3. predefinedDenySet → DROP (all protocols, hard block)
	//   4. Non-TCP: userAllowSet → mark + accept
	//   5. Non-TCP: userDenySet → DROP
	//   6. TCP: continues unmarked, iptables REDIRECT handles proxy
	// ============================================================

	// Rule 1: Allow ESTABLISHED/RELATED connections (mark + accept) - all protocols
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(append(fw.interfaceMatch(),
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
			markAndAccept()...,
		),
	})

	// Rule 2: predefinedAllowSet → mark + accept (all protocols)
	fw.addSetFilterRule(fw.predefinedAllowSet.Set(), false)

	// Rule 3: predefinedDenySet → DROP (all protocols, hard block, NO redirect)
	fw.addSetFilterRule(fw.predefinedDenySet.Set(), true)

	// Rule 4: Non-TCP + userAllowSet → mark + accept
	// Only non-TCP traffic is affected; TCP continues to proxy
	fw.addNonTCPSetFilterRule(fw.userAllowSet.Set(), false)

	// Rule 5: Non-TCP + userDenySet → DROP
	// Only non-TCP traffic is affected; TCP continues to proxy
	fw.addNonTCPSetFilterRule(fw.userDenySet.Set(), true)

	// Default policy: ACCEPT
	// - Non-TCP not in user sets: allowed (default policy)
	// - TCP marking rule is added/removed dynamically

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush nftables changes: %w", err)
	}

	if err := fw.SetTCPFirewall(defaultUseTCPFirewall); err != nil {
		return fmt.Errorf("set default TCP firewall: %w", err)
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
	if err := fw.SetTCPFirewall(defaultUseTCPFirewall); err != nil {
		return fmt.Errorf("clear TCP firewall: %w", err)
	}
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

// SetTCPFirewall controls whether TCP traffic is redirected to the proxy.
func (fw *Firewall) SetTCPFirewall(useTCPFirewall bool) error {
	if useTCPFirewall {
		// Enable TCP rerouting: remove the TCP mark rule if it exists
		if fw.tcpFirewallSkipRule != nil {
			if err := fw.conn.DelRule(fw.tcpFirewallSkipRule); err != nil {
				return fmt.Errorf("delete TCP mark rule: %w", err)
			}
			if err := fw.conn.Flush(); err != nil {
				return fmt.Errorf("flush delete TCP mark rule: %w", err)
			}
			fw.tcpFirewallSkipRule = nil
		}
	} else {
		// Disable TCP rerouting: add a rule to mark all TCP packets as allowed
		if fw.tcpFirewallSkipRule == nil {
			fw.conn.AddRule(&nftables.Rule{
				Table: fw.table,
				Chain: fw.filterChain,
				Exprs: append(append(fw.interfaceMatch(),
					// Match TCP protocol
					&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
					&expr.Cmp{
						Op:       expr.CmpOpEq,
						Register: 1,
						Data:     []byte{unix.IPPROTO_TCP},
					}),
					markAndAccept()...,
				),
			})
			if err := fw.conn.Flush(); err != nil {
				return fmt.Errorf("flush add TCP mark rule: %w", err)
			}
			// Retrieve the rule from the kernel to get its Handle (required for DelRule)
			// AddRule returns a rule without the Handle set; it's only assigned by the kernel after Flush.
			rules, err := fw.conn.GetRules(fw.table, fw.filterChain)
			if err != nil {
				return fmt.Errorf("get rules after adding TCP mark rule: %w", err)
			}
			if len(rules) == 0 {
				return fmt.Errorf("no rules found after adding TCP mark rule")
			}
			// The rule we just added is the last one in the chain
			fw.tcpFirewallSkipRule = rules[len(rules)-1]
		}
	}

	fw.useTCPFirewall = useTCPFirewall

	return nil
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
