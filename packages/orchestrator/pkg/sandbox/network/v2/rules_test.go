//go:build linux

package v2

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/stretchr/testify/require"
)

// Helpers for asserting on installed rules through the same netlink view the
// kernel exposes, so a test fails when a rule's target, port or address is
// wrong — not merely when the chain is missing.

func chainRules(t *testing.T, conn *nftables.Conn, table *nftables.Table, chain string) []*nftables.Rule {
	t.Helper()

	rules, err := conn.GetRules(table, &nftables.Chain{Name: chain, Table: table})
	require.NoError(t, err)

	return rules
}

// natRule reports whether the rule NATs to wantIP with the given NAT type and
// matches wantMatch in the IPv4 header.
func natRule(r *nftables.Rule, natType expr.NATType, wantIP, wantMatch net.IP) bool {
	var toIP, matched bool
	for _, e := range r.Exprs {
		switch v := e.(type) {
		case *expr.NAT:
			if v.Type != natType {
				return false
			}
		case *expr.Immediate:
			toIP = toIP || bytes.Equal(v.Data, wantIP.To4())
		case *expr.Cmp:
			matched = matched || bytes.Equal(v.Data, wantMatch.To4())
		}
	}

	return toIP && matched
}

// redirectPorts returns the matched destination port and the port the rule
// redirects to, for a rule that does both.
func redirectPorts(r *nftables.Rule) (dport, rport uint16, ok bool) {
	var redirects bool
	var ports []uint16

	for _, e := range r.Exprs {
		switch v := e.(type) {
		case *expr.Redir:
			redirects = true
		case *expr.Cmp:
			if len(v.Data) == 2 {
				ports = append(ports, binary.BigEndian.Uint16(v.Data))
			}
		case *expr.Immediate:
			if len(v.Data) == 2 {
				rport = binary.BigEndian.Uint16(v.Data)
			}
		}
	}

	if !redirects || len(ports) == 0 {
		return 0, 0, false
	}

	return ports[len(ports)-1], rport, true
}

// immediatePort returns the port a rule loads into a register, i.e. the port
// it redirects to.
func immediatePort(r *nftables.Rule) uint16 {
	for _, e := range r.Exprs {
		if v, ok := e.(*expr.Immediate); ok && len(v.Data) == 2 {
			return binary.BigEndian.Uint16(v.Data)
		}
	}

	return 0
}

func hasExpr[T expr.Any](r *nftables.Rule) bool {
	for _, e := range r.Exprs {
		if _, ok := e.(T); ok {
			return true
		}
	}

	return false
}

// The in-namespace NAT is the whole point of SetupNamespaceNAT: pin the
// addresses and the direction, not just the chains.
func TestNamespaceNAT_RulesSNATAndDNAT(t *testing.T) { //nolint:paralleltest // creates the nftables table "test-nat-rules" in the host netns
	skipIfNotLinuxRoot(t)

	const (
		hostIP = "10.11.0.1"
		nsIP   = "169.254.0.21"
	)

	conn, err := nftables.New(nftables.AsLasting())
	require.NoError(t, err)
	defer conn.CloseLasting()

	table := conn.AddTable(&nftables.Table{Name: "test-nat-rules", Family: nftables.TableFamilyINet})
	require.NoError(t, conn.Flush())
	t.Cleanup(func() {
		conn.DelTable(table)
		_ = conn.Flush()
	})

	require.NoError(t, SetupNamespaceNAT(conn, table, "eth0", hostIP, nsIP))

	post := chainRules(t, conn, table, "postroute_nat")
	require.Len(t, post, 1)
	require.True(t, natRule(post[0], expr.NATTypeSourceNAT, net.ParseIP(hostIP), net.ParseIP(nsIP)),
		"postrouting must SNAT the namespace IP to the host IP")

	pre := chainRules(t, conn, table, "preroute_nat")
	require.Len(t, pre, 1)
	require.True(t, natRule(pre[0], expr.NATTypeDestNAT, net.ParseIP(nsIP), net.ParseIP(hostIP)),
		"prerouting must DNAT the host IP to the namespace IP")
}

// The host chains are v2's replacement for v1's per-slot iptables rules: pin
// every redirect's port pair and the masquerade.
func TestHostFirewall_ChainRulesMatchConfig(t *testing.T) { //nolint:paralleltest // creates/deletes the singleton nftables table "v2-host-firewall" shared by all HostFirewall tests
	skipIfNotLinuxRoot(t)

	config := testConfig()
	hf := newTestHostFirewall(t, config)

	got := make(map[uint16][]uint16)
	var catchAll []uint16

	for _, r := range chainRules(t, hf.conn, hf.table, "prerouting") {
		dport, rport, ok := redirectPorts(r)
		if !ok {
			// The catch-all matches no port: every remaining TCP is redirected.
			require.True(t, hasExpr[*expr.Redir](r), "every prerouting rule redirects")
			catchAll = append(catchAll, immediatePort(r))

			continue
		}
		got[dport] = append(got[dport], rport)
	}

	require.Equal(t, []uint16{config.SandboxTCPFirewallOtherPort}, catchAll,
		"exactly one catch-all TCP redirect, to the other-port")

	// Port 80 carries two rules: the hyperloop service redirect (matched on the
	// orchestrator IP) first, then the TCP firewall.
	require.Equal(t, []uint16{config.HyperloopProxyPort, config.SandboxTCPFirewallHTTPPort}, got[80])
	require.Equal(t, []uint16{config.PortmapperPort}, got[111])
	require.Equal(t, []uint16{config.NFSProxyPort}, got[2049])
	require.Equal(t, []uint16{config.SandboxTCPFirewallTLSPort}, got[443])

	post := chainRules(t, hf.conn, hf.table, "postrouting")
	require.Len(t, post, 1)
	require.True(t, hasExpr[*expr.Masq](post[0]), "sandbox egress must be masqueraded to the gateway")
}

// One absent element must not stop the other from being removed: an nftables
// batch is atomic, so deleting both in one flush would abort and silently
// leave a stale veth matching the forward and redirect rules.
func TestHostFirewall_RemoveSlotWithOneElementAlreadyGone(t *testing.T) { //nolint:paralleltest // creates/deletes the singleton nftables table "v2-host-firewall" shared by all HostFirewall tests
	skipIfNotLinuxRoot(t)

	ctx := t.Context()
	hf := newTestHostFirewall(t, testConfig())

	slot := makeTestSlot(t, reserveNSTestIdx(t))
	sv2 := NewSlotV2(slot)
	require.NoError(t, hf.AddSlot(ctx, sv2))

	// Drop the CIDR entry behind RemoveSlot's back, as a partially reconciled
	// or half-torn-down host would have.
	hostIP := slot.HostIP.To4()
	hf.conn.SetDeleteElements(hf.cidrSet, []nftables.SetElement{
		{Key: hostIP},
		{Key: incrementIP(hostIP), IntervalEnd: true},
	})
	require.NoError(t, hf.conn.Flush())

	require.NoError(t, hf.RemoveSlot(ctx, sv2))

	require.Error(t, vethSetHas(hf, slot.VethName()),
		"the veth element must be gone even though the CIDR element already was")
}
