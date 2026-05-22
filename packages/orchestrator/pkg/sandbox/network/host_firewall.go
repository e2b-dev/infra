//go:build linux

package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// IFNAMSIZ from <linux/if.h>. nftables `ifname` set keys are null-padded to
// this size.
const ifNameMaxLen = 16

// Names used by the host-side nftables ruleset. They are stable across
// orchestrator restarts so we can replace the table on boot without leaking
// previous rulesets.
const (
	hostFirewallTableName   = "e2b_host"
	hostFirewallVethSetName = "sandbox_veths"
)

// Hook priorities for our table. Both run before the legacy iptables-nft
// compat chains (which sit at priorities 0 / -100), so this ruleset always
// gets first crack at packets and any leftover per-sandbox iptables rules
// from older binaries become harmless no-ops.
const (
	hostForwardPriority    = -10
	hostNATPostroutingPrio = -90
	hostNATPreroutingPrio  = -110
)

// HostFirewall owns the host-side nftables ruleset shared by every sandbox
// on the node. The static chains hook every sandbox's traffic through a
// single `sandbox_veths` ifname set; per-sandbox add/remove is a single
// netlink set-element insert (O(1) regardless of N).
//
// This replaces ~6 per-sandbox iptables shellouts that grew O(N²) under
// iptables-nft (every Append rewrote the whole table under the xtables
// lock).
type HostFirewall struct {
	conn    *nftables.Conn
	table   *nftables.Table
	vethSet *nftables.Set

	mu sync.Mutex // serialises Flush across goroutines
}

// NewHostFirewall installs (or replaces) the table+chains+set on the host
// and returns a handle the network slot lifecycle can use to add / remove
// sandboxes.
func NewHostFirewall(ctx context.Context, config Config, externalIface string) (*HostFirewall, error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("nftables lasting conn: %w", err)
	}

	// Replace any previous instance of our table so a binary downgrade or
	// config change doesn't leave stale rules behind. Both ops are buffered;
	// the real commit happens at Flush below.
	conn.DelTable(&nftables.Table{Name: hostFirewallTableName, Family: nftables.TableFamilyINet})
	if err := conn.Flush(); err != nil && !isNoSuchFileOrDirectory(err) {
		// Allow ENOENT — the table may not have existed yet.
		return nil, fmt.Errorf("delete stale host firewall table: %w", err)
	}

	table := conn.AddTable(&nftables.Table{
		Name:   hostFirewallTableName,
		Family: nftables.TableFamilyINet,
	})

	vethSet := &nftables.Set{
		Table:   table,
		Name:    hostFirewallVethSetName,
		KeyType: nftables.TypeIFName,
	}
	if err := conn.AddSet(vethSet, nil); err != nil {
		return nil, fmt.Errorf("add veth set: %w", err)
	}

	fw := &HostFirewall{conn: conn, table: table, vethSet: vethSet}
	if err := fw.installChains(config, externalIface); err != nil {
		return nil, err
	}
	if err := conn.Flush(); err != nil {
		return nil, fmt.Errorf("flush host firewall table: %w", err)
	}
	return fw, nil
}

// AddSandbox inserts the given veth interface name into the sandbox set so
// the global forward / nat rules start matching traffic from this sandbox.
// One netlink message + one commit; O(1) regardless of fleet size.
func (h *HostFirewall) AddSandbox(vethName string) error {
	key, err := ifnameKey(vethName)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.conn.SetAddElements(h.vethSet, []nftables.SetElement{{Key: key}}); err != nil {
		return fmt.Errorf("add veth %q to set: %w", vethName, err)
	}
	if err := h.conn.Flush(); err != nil {
		return fmt.Errorf("flush veth %q add: %w", vethName, err)
	}
	return nil
}

// RemoveSandbox removes the given veth interface name from the sandbox set.
// Missing elements are not an error: teardown is best-effort.
func (h *HostFirewall) RemoveSandbox(vethName string) error {
	key, err := ifnameKey(vethName)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.conn.SetDeleteElements(h.vethSet, []nftables.SetElement{{Key: key}}); err != nil {
		return fmt.Errorf("delete veth %q from set: %w", vethName, err)
	}
	if err := h.conn.Flush(); err != nil {
		if isNoSuchFileOrDirectory(err) {
			return nil
		}
		return fmt.Errorf("flush veth %q remove: %w", vethName, err)
	}
	return nil
}

// Close releases the lasting netlink connection. The kernel ruleset stays
// in place so existing sandboxes keep working across orchestrator restarts.
func (h *HostFirewall) Close() error {
	if h == nil || h.conn == nil {
		return nil
	}
	return h.conn.CloseLasting()
}

func (h *HostFirewall) installChains(config Config, externalIface string) error {
	policyAccept := nftables.ChainPolicyAccept

	forward := h.conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    h.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityRef(hostForwardPriority),
		Policy:   &policyAccept,
	})

	postroutingNAT := h.conn.AddChain(&nftables.Chain{
		Name:     "postrouting_nat",
		Table:    h.table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityRef(hostNATPostroutingPrio),
		Policy:   &policyAccept,
	})

	preroutingNAT := h.conn.AddChain(&nftables.Chain{
		Name:     "prerouting_nat",
		Table:    h.table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(hostNATPreroutingPrio),
		Policy:   &policyAccept,
	})

	ifname := append([]byte(externalIface), 0)

	// forward: iifname @sandbox_veths && oifname == external → accept
	h.conn.AddRule(&nftables.Rule{
		Table: h.table, Chain: forward,
		Exprs: concat(
			matchIifnameInSet(h.vethSet),
			matchMetaEq(expr.MetaKeyOIFNAME, ifname),
			verdictAccept(),
		),
	})
	// forward: iifname == external && oifname @sandbox_veths → accept
	h.conn.AddRule(&nftables.Rule{
		Table: h.table, Chain: forward,
		Exprs: concat(
			matchMetaEq(expr.MetaKeyIIFNAME, ifname),
			matchOifnameInSet(h.vethSet),
			verdictAccept(),
		),
	})

	// postrouting nat: ip saddr in hostNetworkCIDR && oifname == external → masquerade
	saddrMatch, err := matchIPv4SrcInCIDR(hostNetworkCIDR)
	if err != nil {
		return fmt.Errorf("masquerade source match: %w", err)
	}
	h.conn.AddRule(&nftables.Rule{
		Table: h.table, Chain: postroutingNAT,
		Exprs: concat(
			saddrMatch,
			matchMetaEq(expr.MetaKeyOIFNAME, ifname),
			[]expr.Any{&expr.Masq{}},
		),
	})

	// prerouting nat: host services exposed to sandboxes (global, dst-IP keyed).
	orchIP := net.ParseIP(config.OrchestratorInSandboxIPAddress).To4()
	if orchIP == nil {
		return fmt.Errorf("invalid OrchestratorInSandboxIPAddress %q", config.OrchestratorInSandboxIPAddress)
	}
	hostServiceRules := []struct {
		port    uint16
		toPort  uint16
		comment string
	}{
		{port: 80, toPort: config.HyperloopProxyPort, comment: "hyperloop"},
		{port: 111, toPort: config.PortmapperPort, comment: "portmapper"},
		{port: 2049, toPort: config.NFSProxyPort, comment: "nfs-proxy"},
	}
	for _, r := range hostServiceRules {
		h.conn.AddRule(&nftables.Rule{
			Table: h.table, Chain: preroutingNAT,
			Exprs: concat(
				matchIPv4DstEq(orchIP),
				matchTCPDport(r.port),
				redirectTo(r.toPort),
			),
		})
	}

	// prerouting nat: per-sandbox TCP firewall.
	tcpfwRules := []struct {
		dport  uint16 // 0 = catchall
		toPort uint16
	}{
		{dport: 80, toPort: config.SandboxTCPFirewallHTTPPort},
		{dport: 443, toPort: config.SandboxTCPFirewallTLSPort},
		{dport: 0, toPort: config.SandboxTCPFirewallOtherPort},
	}
	for _, r := range tcpfwRules {
		var match []expr.Any
		if r.dport != 0 {
			match = concat(matchIifnameInSet(h.vethSet), matchTCPDport(r.dport))
		} else {
			match = concat(matchIifnameInSet(h.vethSet), matchL4Proto(unix.IPPROTO_TCP))
		}
		h.conn.AddRule(&nftables.Rule{
			Table: h.table, Chain: preroutingNAT,
			Exprs: append(match, redirectTo(r.toPort)...),
		})
	}

	return nil
}

// Expression helpers below. Each returns a slice that can be concatenated
// into a Rule.Exprs.

func matchMetaEq(key expr.MetaKey, want []byte) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: key, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: want},
	}
}

func matchIifnameInSet(s *nftables.Set) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetID: s.ID, SetName: s.Name},
	}
}

func matchOifnameInSet(s *nftables.Set) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetID: s.ID, SetName: s.Name},
	}
}

func matchIPv4SrcInCIDR(cidr *net.IPNet) ([]expr.Any, error) {
	ip4 := cidr.IP.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("not IPv4: %s", cidr)
	}
	mask := net.IP(cidr.Mask).To4()
	if mask == nil {
		return nil, fmt.Errorf("not IPv4 mask: %s", cidr)
	}
	return []expr.Any{
		// payload: ip saddr (offset 12, length 4)
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseNetworkHeader,
			Offset:       12,
			Len:          4,
		},
		// mask saddr
		&expr.Bitwise{
			SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: mask, Xor: []byte{0, 0, 0, 0},
		},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip4.Mask(cidr.Mask)},
	}, nil
}

func matchIPv4DstEq(ip net.IP) []expr.Any {
	return []expr.Any{
		// payload: ip daddr (offset 16, length 4)
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseNetworkHeader,
			Offset:       16,
			Len:          4,
		},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip},
	}
}

func matchL4Proto(proto byte) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
	}
}

func matchTCPDport(port uint16) []expr.Any {
	return concat(
		matchL4Proto(unix.IPPROTO_TCP),
		[]expr.Any{
			// payload: tcp dport (offset 2 in transport header, length 2)
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseTransportHeader,
				Offset:       2,
				Len:          2,
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
		},
	)
}

func redirectTo(port uint16) []expr.Any {
	return []expr.Any{
		&expr.Immediate{Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
		&expr.Redir{RegisterProtoMin: 1, RegisterProtoMax: 1},
	}
}

func verdictAccept() []expr.Any {
	return []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}}
}

func concat(parts ...[]expr.Any) []expr.Any {
	var out []expr.Any
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ifnameKey null-pads an interface name to IFNAMSIZ for use as an nftables
// `ifname` set key.
func ifnameKey(name string) ([]byte, error) {
	if len(name) >= ifNameMaxLen {
		return nil, fmt.Errorf("interface name %q too long (max %d bytes)", name, ifNameMaxLen-1)
	}
	key := make([]byte, ifNameMaxLen)
	copy(key, name)
	return key, nil
}

func isNoSuchFileOrDirectory(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such file or directory")
}
