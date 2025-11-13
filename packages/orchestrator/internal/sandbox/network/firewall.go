package network

import (
	"fmt"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/ngrok/firewall_toolkit/pkg/expressions"
	"github.com/ngrok/firewall_toolkit/pkg/rule"
	"github.com/ngrok/firewall_toolkit/pkg/set"

	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

const (
	tableName = "slot-firewall"
)

type Firewall struct {
	conn  *nftables.Conn
	table *nftables.Table
	chain *nftables.Chain

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
	acceptPolicy := nftables.ChainPolicyAccept
	chain := conn.AddChain(&nftables.Chain{
		Name:     "FORWARD",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &acceptPolicy,
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
		chain:              chain,
		predefinedDenySet:  alwaysDenySet,
		predefinedAllowSet: alwaysAllowSet,
		userDenySet:        denySet,
		userAllowSet:       allowSet,
		tapInterface:       tapIf,
		allowedRanges:      []string{fmt.Sprintf("%s/32", hyperloopIP)},
	}

	// Add firewall rules to the chain
	if err := fw.installRules(); err != nil {
		return nil, err
	}

	// Populate the sets with initial data
	err = fw.ResetAllSets()
	if err != nil {
		return nil, fmt.Errorf("error while configuring initial data: %w", err)
	}

	return fw, nil
}

func (fw *Firewall) Close() error {
	return fw.conn.CloseLasting()
}

func (fw *Firewall) installRules() error {
	m := fw.tapInterface

	// helper for the tap interface
	ifaceMatch := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{
			Register: 1,
			Op:       expr.CmpOpEq,
			Data:     append([]byte(m), 0), // null-terminated
		},
	}

	// Allow ESTABLISHED,RELATED
	exprs, err := rule.Build(
		expr.VerdictAccept,
		rule.TransportProtocol(expressions.TCP),
		rule.LoadConnectionTrackingState(expr.CtKeySTATE),
		rule.ConnectionTrackingState(expr.CtStateBitRELATED|expr.CtStateBitESTABLISHED),
	)
	if err != nil {
		return fmt.Errorf("build rule for established/related: %w", err)
	}
	fw.conn.InsertRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			exprs...,
		),
	})

	// The order should be
	// 1. Allow anything in predefinedAllowSet
	// 2. Deny anything in predefinedDenySet
	// 3. Allow anything in userAllowSet
	// 4. Deny anything in userDenySet

	// Accept anything in predefinedAllowSet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.predefinedAllowSet.Set(), 1),
			expressions.Accept(),
		),
	})

	// Drop anything in predefinedDenySet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.predefinedDenySet.Set(), 1),
			expressions.Drop(),
		),
	})

	// Allow anything in userAllowSet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.userAllowSet.Set(), 1),
			expressions.Accept(),
		),
	})

	// Drop anything in userDenySet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.userDenySet.Set(), 1),
			expressions.Drop(),
		),
	})

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

func (fw *Firewall) ResetAllSets() error {
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

	if len(current) == 1 && current[0].AddressRangeStart == netip.MustParseAddr("0.0.0.0") && current[0].AddressRangeEnd == netip.MustParseAddr("255.255.255.255") {
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
