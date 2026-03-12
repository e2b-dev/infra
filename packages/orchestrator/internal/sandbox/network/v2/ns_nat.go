package v2

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// SetupNamespaceNAT adds SNAT/DNAT chains to the existing "slot-firewall" nftables
// table inside a network namespace. This replaces the two iptables rules that v1 creates:
//   - iptables -t nat POSTROUTING -o eth0 -s <namespaceIP> -j SNAT --to <hostIP>
//   - iptables -t nat PREROUTING  -i eth0 -d <hostIP>      -j DNAT --to <namespaceIP>
//
// The nftables equivalent uses the same kernel conntrack, so SO_ORIGINAL_DST
// in the TCP firewall proxy continues to work unchanged.
func SetupNamespaceNAT(conn *nftables.Conn, table *nftables.Table, vpeerIface, hostIP, namespaceIP string) error {
	hostIPParsed := net.ParseIP(hostIP).To4()
	if hostIPParsed == nil {
		return fmt.Errorf("invalid host IP: %s", hostIP)
	}

	nsIPParsed := net.ParseIP(namespaceIP).To4()
	if nsIPParsed == nil {
		return fmt.Errorf("invalid namespace IP: %s", namespaceIP)
	}

	// POSTROUTING NAT chain: SNAT namespace IP → host IP
	postChain := conn.AddChain(&nftables.Chain{
		Name:     "postroute_nat",
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
	})

	// Match: oifname == vpeerIface && ip saddr == namespaceIP → snat to hostIP
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: postChain,
		Exprs: []expr.Any{
			// Match output interface
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     ifnameBytes(vpeerIface),
			},
			// Match source IP == namespaceIP
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       12, // src IP offset in IPv4
				Len:          4,
			},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     nsIPParsed,
			},
			// SNAT to hostIP
			&expr.Immediate{
				Register: 1,
				Data:     hostIPParsed,
			},
			&expr.NAT{
				Type:        expr.NATTypeSourceNAT,
				Family:      unix.NFPROTO_IPV4,
				RegAddrMin:  1,
				RegAddrMax:  1,
				RegProtoMin: 0,
				RegProtoMax: 0,
			},
		},
	})

	// PREROUTING NAT chain: DNAT hostIP → namespaceIP
	preChain := conn.AddChain(&nftables.Chain{
		Name:     "preroute_nat",
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityNATDest,
	})

	// Match: iifname == vpeerIface && ip daddr == hostIP → dnat to namespaceIP
	conn.AddRule(&nftables.Rule{
		Table: table,
		Chain: preChain,
		Exprs: []expr.Any{
			// Match input interface
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     ifnameBytes(vpeerIface),
			},
			// Match destination IP == hostIP
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       16, // dst IP offset in IPv4
				Len:          4,
			},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     hostIPParsed,
			},
			// DNAT to namespaceIP
			&expr.Immediate{
				Register: 1,
				Data:     nsIPParsed,
			},
			&expr.NAT{
				Type:        expr.NATTypeDestNAT,
				Family:      unix.NFPROTO_IPV4,
				RegAddrMin:  1,
				RegAddrMax:  1,
				RegProtoMin: 0,
				RegProtoMax: 0,
			},
		},
	})

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("flush namespace NAT rules: %w", err)
	}

	return nil
}

// ifnameBytes pads an interface name to IFNAMSIZ (16 bytes) with null termination.
func ifnameBytes(name string) []byte {
	b := make([]byte, 16) // IFNAMSIZ
	copy(b, name)
	return b
}
