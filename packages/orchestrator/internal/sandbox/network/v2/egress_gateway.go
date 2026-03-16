package v2

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const egressGwTableName = "egress-gateway"

// SNATRule defines a source NAT rule on the egress gateway.
type SNATRule struct {
	SourceCIDR string // e.g., "10.11.0.0/16" (sandbox traffic from compute node)
	SNATIP     net.IP // sticky IP to SNAT to
	FwMark     uint32 // optional: match specific profile mark (0 = match all)
}

// EgressGatewayConfig configures the gateway side (runs on box for PoC).
type EgressGatewayConfig struct {
	WgInterface   string // "wg0"
	ExternalIface string // "enp0s2"
	SNATRules     []SNATRule
}

// SetupEgressGateway creates nftables rules on the gateway node for
// forwarding traffic from WireGuard and applying SNAT.
//
// Creates table "egress-gateway" with:
//   - forward chain: wg0 → external accept, external → wg0 ct established accept
//   - postrouting chain: SNAT rules per source CIDR
func SetupEgressGateway(cfg EgressGatewayConfig) error {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return fmt.Errorf("new nftables conn: %w", err)
	}
	defer conn.CloseLasting()

	// Ensure table exists (idempotent). We do NOT delete the existing table
	// to avoid disrupting live gateway traffic during restarts.
	table := conn.AddTable(&nftables.Table{
		Name:   egressGwTableName,
		Family: nftables.TableFamilyINet,
	})

	wgBytes := ifnameBytes(cfg.WgInterface)
	extBytes := ifnameBytes(cfg.ExternalIface)

	// --- Forward chain ---
	fwdPolicy := nftables.ChainPolicyAccept
	fwdChain := conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &fwdPolicy,
	})
	conn.FlushChain(fwdChain) // refresh rules with current config

	// wg0 → external: accept
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: fwdChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: wgBytes},
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: extBytes},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	})

	// external → wg0: ct state established,related accept
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: fwdChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: extBytes},
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: wgBytes},
			&expr.Ct{Key: expr.CtKeySTATE, Register: 1},
			&expr.Bitwise{
				SourceRegister: 1, DestRegister: 1, Len: 4,
				Mask: bitmask32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
				Xor:  bitmask32(0),
			},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: bitmask32(0)},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	})

	// --- Postrouting chain (NAT) ---
	postChain := conn.AddChain(&nftables.Chain{
		Name:     "postrouting",
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
	})
	conn.FlushChain(postChain)

	for _, rule := range cfg.SNATRules {
		_, srcNet, err := net.ParseCIDR(rule.SourceCIDR)
		if err != nil {
			return fmt.Errorf("parse SNAT source CIDR %s: %w", rule.SourceCIDR, err)
		}

		snatIP := rule.SNATIP.To4()
		if snatIP == nil {
			return fmt.Errorf("invalid SNAT IP: %v", rule.SNATIP)
		}

		ones, _ := srcNet.Mask.Size()

		exprs := []expr.Any{
			// Match output interface
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: extBytes},
			// Match source CIDR
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           net.CIDRMask(ones, 32),
				Xor:            []byte{0, 0, 0, 0},
			},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: srcNet.IP.To4()},
		}

		// Optional fwmark matching for per-profile SNAT
		if rule.FwMark != 0 {
			exprs = append(exprs,
				&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
				&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: bitmask32(rule.FwMark)},
			)
		}

		// SNAT to sticky IP
		exprs = append(exprs,
			&expr.Immediate{Register: 1, Data: snatIP},
			&expr.NAT{
				Type:       expr.NATTypeSourceNAT,
				Family:     unix.NFPROTO_IPV4,
				RegAddrMin: 1,
				RegAddrMax: 1,
			},
		)

		conn.AddRule(&nftables.Rule{
			Table: table,
			Chain: postChain,
			Exprs: exprs,
		})
	}

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("flush egress gateway rules: %w", err)
	}

	return nil
}

// TeardownEgressGateway removes the egress gateway nftables table.
func TeardownEgressGateway() error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("new nftables conn: %w", err)
	}

	conn.DelTable(&nftables.Table{Name: egressGwTableName, Family: nftables.TableFamilyINet})

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("delete egress gateway table: %w", err)
	}

	return nil
}
