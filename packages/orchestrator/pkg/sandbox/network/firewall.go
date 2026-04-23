package network

import (
	"fmt"
	"net/netip"
	"slices"
	"sync/atomic"

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

	// byopEnabled tracks whether Rule 3 is currently in BYOP mode (drop
	// non-TCP only) vs default (drop all protocols). Slot pool reuse must
	// flip this back to default before handing the slot to a non-BYOP
	// sandbox; see EnableBYOPProxy / DisableBYOPProxy.
	byopEnabled atomic.Bool
}

func NewFirewall(tapIf string, orchestratorInternalIP string, extraAllowedCIDRs []string) (*Firewall, error) {
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
		allowedRanges: append(
			[]string{fmt.Sprintf("%s/32", orchestratorInternalIP)},
			extraAllowedCIDRs...,
		),
		filterChain: filterChain,
	}

	// Add firewall rules to the chain. byop=false: kernel-level drop on
	// predefinedDenySet applies to all protocols (defense-in-depth alongside
	// userspace tcpfw). Sandboxes with egressProxy configured later switch
	// to byop=true via EnableBYOPProxy.
	if err := fw.installRules(false); err != nil {
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
// This applies to ALL protocols. A counter is attached for observability
// (visible via `nft list chain inet slot-firewall PREROUTE_FILTER`).
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
			expressions.IPSetLookUp(ipSet, 1),
			&expr.Counter{}),
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
			expressions.IPSetLookUp(ipSet, 1),
			&expr.Counter{}),
			verdict...,
		),
	})
}

// installRules installs the filter chain. byop controls Rule 3:
//
//	false → predefinedDenySet → DROP (all protocols). Kernel enforces the
//	        deny set for TCP and non-TCP alike. Default for sandboxes
//	        without an egress proxy.
//	true  → predefinedDenySet → DROP (non-TCP only). TCP enforcement for
//	        these ranges shifts to userspace tcpfw, which routes via the
//	        user-supplied SOCKS5 proxy and applies a dial-time DNS-rebind
//	        guard. Set contents are unchanged across modes.
func (fw *Firewall) installRules(byop bool) error {
	// ============================================================
	// FILTER CHAIN (PREROUTING, priority -150)
	// Order:
	//   1. ESTABLISHED/RELATED → accept (allow responses even from denied ranges)
	//   2. predefinedAllowSet → accept (all protocols; includes orchestrator IP)
	//   3. predefinedDenySet → DROP. All protocols by default; non-TCP only
	//      when byop=true. See function-level comment.
	//   4. Non-TCP: userAllowSet → accept
	//   5. Non-TCP: userDenySet → DROP
	//   6. Default: ACCEPT (TCP handled by iptables REDIRECT in host netns)
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
			},
			&expr.Counter{}),
			accept()...,
		),
	})

	// Rule 2: predefinedAllowSet → accept (all protocols)
	fw.addSetFilterRule(fw.predefinedAllowSet.Set(), false)

	// Rule 3: predefinedDenySet → DROP.
	// Default (byop=false): drop all protocols at the kernel; tcpfw in the
	// host netns is a redundant userspace check.
	// BYOP (byop=true): drop non-TCP only. TCP for predefined-deny ranges
	// is routed by the host-netns iptables REDIRECT to tcpfw, which dials
	// via the user-supplied SOCKS5 proxy. The dial-time DNS-rebind guard
	// in newSOCKS5DialContext rejects rebinds onto these ranges.
	if byop {
		fw.addNonTCPSetFilterRule(fw.predefinedDenySet.Set(), true)
	} else {
		fw.addSetFilterRule(fw.predefinedDenySet.Set(), true)
	}

	// Rule 4: Non-TCP + userAllowSet → accept
	// Only non-TCP traffic is affected; TCP goes to proxy
	fw.addNonTCPSetFilterRule(fw.userAllowSet.Set(), false)

	// Rule 5: Non-TCP + userDenySet → DROP
	// Only non-TCP traffic is affected; TCP goes to proxy
	fw.addNonTCPSetFilterRule(fw.userDenySet.Set(), true)

	// Default policy: ACCEPT
	// - Non-TCP not in user sets: allowed (default policy)
	// - TCP: iptables REDIRECT handles TCP traffic to proxy

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush nftables changes: %w", err)
	}

	return nil
}

// EnableBYOPProxy switches the filter chain into BYOP mode by atomically
// flushing all rules and reinstalling them with Rule 3 narrowed to non-TCP.
// Called from slot.ConfigureInternet when the sandbox has egressProxy set.
// Idempotent — no-op when already in BYOP mode.
func (fw *Firewall) EnableBYOPProxy() error {
	if !fw.byopEnabled.CompareAndSwap(false, true) {
		return nil
	}
	fw.conn.FlushChain(fw.filterChain)
	if err := fw.installRules(true); err != nil {
		// Roll back so a subsequent call retries the install.
		fw.byopEnabled.Store(false)
		return err
	}
	return nil
}

// DisableBYOPProxy reverts the filter chain from BYOP mode back to default
// (Rule 3 drops all protocols). Called from slot.ResetInternet during pool
// recycle so a non-BYOP sandbox does not inherit a narrowed kernel firewall
// from a previous BYOP tenant. Idempotent — no-op when already default.
func (fw *Firewall) DisableBYOPProxy() error {
	if !fw.byopEnabled.CompareAndSwap(true, false) {
		return nil
	}
	fw.conn.FlushChain(fw.filterChain)
	if err := fw.installRules(false); err != nil {
		// Roll back: state is uncertain, leave byopEnabled=true so a
		// subsequent ResetInternet retries the disable.
		fw.byopEnabled.Store(true)
		return err
	}
	return nil
}

// ReplaceUserRules atomically replaces all firewall sets in a single flush.
// This avoids any window where rules are partially applied.
func (fw *Firewall) ReplaceUserRules(allowedCIDRs, deniedCIDRs []string) error {
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

	// 3. Replace user deny set with new denied CIDRs (buffered, no flush).
	if err := clearAndReplaceCIDRs(fw.conn, fw.userDenySet, deniedCIDRs); err != nil {
		return fmt.Errorf("replace user deny set: %w", err)
	}

	// 4. Replace user allow set with new allowed CIDRs (buffered, no flush).
	if err := clearAndReplaceCIDRs(fw.conn, fw.userAllowSet, allowedCIDRs); err != nil {
		return fmt.Errorf("replace user allow set: %w", err)
	}

	// 5. Single atomic flush.
	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush atomic rule replacement: %w", err)
	}

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
