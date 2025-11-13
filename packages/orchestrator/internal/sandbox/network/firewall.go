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

	blockInternetTraffic bool
	denySet              set.Set
	allowSet             set.Set
	tapInterface         string

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
	denySet, err := set.New(conn, table, "filtered_denylist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new deny set: %w", err)
	}
	allowSet, err := set.New(conn, table, "filtered_allowlist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new allow set: %w", err)
	}

	fw := &Firewall{
		conn:                 conn,
		table:                table,
		chain:                chain,
		blockInternetTraffic: false,
		denySet:              denySet,
		allowSet:             allowSet,
		tapInterface:         tapIf,
		allowedRanges:        []string{fmt.Sprintf("%s/32", hyperloopIP)},
	}

	// Add firewall rules to the chain
	if err := fw.installRules(); err != nil {
		return nil, err
	}

	// Populate the sets with initial data
	err = fw.ResetAllCustom()
	if err != nil {
		return nil, fmt.Errorf("error while configuring initial deny set: %w", err)
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

	// Allow anything in allowSet
	fw.conn.InsertRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.allowSet.Set(), 1),
			expressions.Accept(),
		),
	})

	// Drop anything in denySet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.denySet.Set(), 1),
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
	if fw.blockInternetTraffic {
		// If internet is denied, we don't need to add any other addresses to the deny set.
		// Because 0.0.0.0/0 is not valid IP per GoLang, we can't add new addresses to the deny set.
		return nil
	}

	// 0.0.0.0/0 is not valid IP per GoLang, so we handle it as a special case
	if cidr == sandbox_network.AllInternetTrafficCIDR {
		fw.blockInternetTraffic = true

		fw.conn.FlushSet(fw.denySet.Set())

		toAppend := []nftables.SetElement{
			{Key: netip.MustParseAddr("0.0.0.0").AsSlice()},
			{
				Key:         netip.MustParseAddr("255.255.255.255").AsSlice(),
				IntervalEnd: true,
			},
		}

		if err := fw.conn.SetAddElements(fw.denySet.Set(), toAppend); err != nil {
			return fmt.Errorf("add elements to denied set: %w", err)
		}
	} else {
		current, err := fw.denySet.Elements(fw.conn)
		if err != nil {
			return err
		}

		data, err := set.AddressStringsToSetData([]string{cidr})
		if err != nil {
			return err
		}
		merged := append(current, data...)
		if err := fw.denySet.ClearAndAddElements(fw.conn, merged); err != nil {
			return err
		}
	}

	err := fw.conn.Flush()
	if err != nil {
		return fmt.Errorf("flush add denied cidr changes: %w", err)
	}

	return nil
}

// AddAllowedCIDR adds a single CIDR to the allow set at runtime.
func (fw *Firewall) AddAllowedCIDR(cidr string) error {
	err := sandbox_network.CanAllowCIDR(cidr)
	if err != nil {
		return err
	}

	data, err := set.AddressStringsToSetData([]string{cidr})
	if err != nil {
		return err
	}
	current, err := fw.allowSet.Elements(fw.conn)
	if err != nil {
		return err
	}
	merged := append(current, data...)
	if err := fw.allowSet.ClearAndAddElements(fw.conn, merged); err != nil {
		return err
	}

	err = fw.conn.Flush()
	if err != nil {
		return fmt.Errorf("flush add allowed IP changes: %w", err)
	}

	return nil
}

func (fw *Firewall) ResetAllCustom() error {
	if err := fw.ResetDeniedCustom(); err != nil {
		return fmt.Errorf("clear block set: %w", err)
	}
	if err := fw.ResetAllowedCustom(); err != nil {
		return fmt.Errorf("clear allow set: %w", err)
	}

	return nil
}

// ResetDeniedCustom resets the deny set back to original ranges.
func (fw *Firewall) ResetDeniedCustom() error {
	initData, err := set.AddressStringsToSetData(sandbox_network.DeniedSandboxCIDRs)
	if err != nil {
		return fmt.Errorf("parse initial denied CIDRs: %w", err)
	}

	if err := fw.denySet.ClearAndAddElements(fw.conn, initData); err != nil {
		return err
	}

	fw.blockInternetTraffic = false

	return fw.conn.Flush()
}

// ResetAllowedCustom resets allow set back to original ranges.
func (fw *Firewall) ResetAllowedCustom() error {
	initData, err := set.AddressStringsToSetData(fw.allowedRanges)
	if err != nil {
		return fmt.Errorf("parse initial allowed CIDRs: %w", err)
	}

	if err := fw.allowSet.ClearAndAddElements(fw.conn, initData); err != nil {
		return err
	}

	return fw.conn.Flush()
}
