package network

import (
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/ngrok/firewall_toolkit/pkg/expressions"
	"github.com/ngrok/firewall_toolkit/pkg/rule"
	"github.com/ngrok/firewall_toolkit/pkg/set"
)

// your original CIDR list
var blockedRanges = []string{
	"10.0.0.0/8",
	"169.254.0.0/16",
	"192.168.0.0/16",
	"172.16.0.0/12",
}

// Firewall wraps a nftables.Conn + two dynamic sets.
type Firewall struct {
	conn         *nftables.Conn
	table        *nftables.Table
	chain        *nftables.Chain
	blockSet     set.Set
	allowSet     set.Set
	tapInterface string
}

// NewFirewall creates the table, chain, sets, seeds the block-set,
// installs all rules, and flushes to kernel.
func NewFirewall(tapIf string) (*Firewall, error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("new nftables conn: %w", err)
	}

	// 1) create an "inet" table and a forward chain
	table := conn.AddTable(&nftables.Table{
		Name:   "slot-firewall",
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

	// 2) build the block-set and allow-set
	blockSet, err := set.New(conn, table, "filtered_blocklist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new block set: %w", err)
	}
	allowSet, err := set.New(conn, table, "filtered_allowlist", nftables.TypeIPAddr)
	if err != nil {
		return nil, fmt.Errorf("new allow set: %w", err)
	}

	// populate allow set

	fw := &Firewall{
		conn:         conn,
		table:        table,
		chain:        chain,
		blockSet:     blockSet,
		allowSet:     allowSet,
		tapInterface: tapIf,
	}

	// 3) install the three top-level rules
	if err := fw.installRules(); err != nil {
		return nil, err
	}

	// populate default block set with the original CIDRs
	err = fw.ResetAllCustom()
	if err != nil {
		return nil, fmt.Errorf("clear initial block set: %w", err)
	}
	return fw, nil
}

func (fw *Firewall) Close() error {
	return fw.conn.CloseLasting()
}

// installRules wires up: (i) established → ACCEPT, (ii) in allowSet → ACCEPT,
// then if !allowInternet a catch-all DROP, and (iii) in blockSet → DROP.
func (fw *Firewall) installRules() error {
	m := fw.tapInterface

	// helper for matching our tap interface
	ifaceMatch := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{
			Register: 1,
			Op:       expr.CmpOpEq,
			Data:     append([]byte(m), 0), // null-terminated
		},
	}

	// A) allow ESTABLISHED,RELATED
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

	// B) allow anything in our allowSet
	fw.conn.InsertRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.allowSet.Set(), 1),
			expressions.Accept(),
		),
	})

	// D) drop anything in blockSet
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.chain,
		Exprs: append(ifaceMatch,
			expressions.IPv4DestinationAddress(1),
			expressions.IPSetLookUp(fw.blockSet.Set(), 1),
			expressions.Drop(),
		),
	})

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush nftables changes: %w", err)
	}

	return nil
}

// AddBlockedIP adds a single CIDR to the block set at runtime.
func (fw *Firewall) AddBlockedIP(cidr string) error {
	if cidr == "0.0.0.0/0" {
		fw.conn.FlushSet(fw.blockSet.Set())

		toAppend := []nftables.SetElement{
			{Key: netip.MustParseAddr("0.0.0.0").AsSlice()},
			{Key: netip.MustParseAddr("255.255.255.255").AsSlice(),
				IntervalEnd: true},
		}

		// add everything in newSetData to the set
		if err := fw.conn.SetAddElements(fw.blockSet.Set(), toAppend); err != nil {
			return fmt.Errorf("add elements to block set: %w", err)
		}
	} else {
		current, err := fw.blockSet.Elements(fw.conn)
		if err != nil {
			return err
		}

		data, err := set.AddressStringsToSetData([]string{cidr})
		if err != nil {
			return err
		}
		merged := append(current, data...)
		if err := fw.blockSet.ClearAndAddElements(fw.conn, merged); err != nil {
			return err
		}
	}

	err := fw.conn.Flush()
	if err != nil {
		return fmt.Errorf("flush add blocked IP changes: %w", err)
	}
	return nil
}

// AddAllowedIP adds a single CIDR to the allow set at runtime.
func (fw *Firewall) AddAllowedIP(cidr string) error {
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
	if err := fw.ResetBlockedCustom(); err != nil {
		return fmt.Errorf("clear block set: %w", err)
	}
	if err := fw.ResetAllowedCustom(); err != nil {
		return fmt.Errorf("clear allow set: %w", err)
	}

	return nil
}

// ResetBlockedCustom resets the block set back to your original four ranges.
func (fw *Firewall) ResetBlockedCustom() error {
	initData, err := set.AddressStringsToSetData(blockedRanges)
	if err != nil {
		return fmt.Errorf("parse initial block CIDRs: %w", err)
	}

	if err := fw.blockSet.ClearAndAddElements(fw.conn, initData); err != nil {
		return err
	}
	return fw.conn.Flush()
}

// ResetAllowedCustom wipes out everything from the allow set.
func (fw *Firewall) ResetAllowedCustom() error {
	initIps := make([]string, 0)
	if ip := os.Getenv("LOGS_COLLECTOR_IP"); ip != "" {
		ip = strings.TrimPrefix(ip, "http://") + "/32"
		initIps = append(initIps, ip)
	}

	initData, err := set.AddressStringsToSetData(initIps)
	if err != nil {
		return fmt.Errorf("parse initial allow CIDRs: %w", err)
	}
	if err := fw.allowSet.ClearAndAddElements(fw.conn, initData); err != nil {
		return err
	}
	return fw.conn.Flush()
}
