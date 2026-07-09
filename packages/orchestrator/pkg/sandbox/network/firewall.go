//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/ngrok/firewall_toolkit/pkg/expressions"
	"github.com/ngrok/firewall_toolkit/pkg/set"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

const tableName = "slot-firewall"

type Firewall struct {
	// mu serializes the shared conn buffer, which is committed on Flush().
	mu sync.Mutex

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

func NewFirewall(tapIf string, orchestratorInternalIP string, extraAllowedCIDRs []string) (_ *Firewall, err error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("new nftables conn: %w", err)
	}

	defer func() {
		if err != nil {
			err = errors.Join(err, conn.CloseLasting())
		}
	}()

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

	controlIP := fmt.Sprintf("%s/32", orchestratorInternalIP)

	fw := &Firewall{
		conn:               conn,
		table:              table,
		predefinedDenySet:  alwaysDenySet,
		predefinedAllowSet: alwaysAllowSet,
		userDenySet:        denySet,
		userAllowSet:       allowSet,
		tapInterface:       tapIf,
		allowedRanges: append(
			[]string{controlIP},
			extraAllowedCIDRs...,
		),
		filterChain: filterChain,
	}

	// Install default rules and initial set data in a single flush.
	fw.installRules(false)
	if err := fw.bufferUserRules(nil, nil); err != nil {
		return nil, fmt.Errorf("error while configuring initial data: %w", err)
	}
	if err := fw.conn.Flush(); err != nil {
		return nil, fmt.Errorf("flush initial firewall rules: %w", err)
	}

	return fw, nil
}

func (fw *Firewall) Close() error {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	fw.conn.DelTable(&nftables.Table{
		Name:   tableName,
		Family: nftables.TableFamilyINet,
	})
	deleteErr := fw.conn.Flush()
	if errors.Is(deleteErr, unix.ENOENT) {
		deleteErr = nil
	}

	return errors.Join(deleteErr, fw.conn.CloseLasting())
}

// resetConn replaces fw.conn with a fresh netlink connection, discarding
// buffered messages and any sticky serialization error — nftables.Conn has no
// API for that (see https://github.com/google/nftables/pull/324).
func (fw *Firewall) resetConn(ctx context.Context) error {
	closeErr := fw.conn.CloseLasting()

	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		err = fmt.Errorf("open new lasting nftables conn: %w", err)

		// Fall back to a transient conn.
		var transientErr error
		conn, transientErr = nftables.New()
		if transientErr != nil {
			err = errors.Join(err, fmt.Errorf("open transient nftables conn: %w", transientErr))
		}
	}

	resetErr := errors.Join(closeErr, err)
	switch {
	case conn == nil:
		// Both the lasting and transient constructors failed. Keep the old
		// (already closed, possibly poisoned) conn rather than storing nil and
		// panicking on next use; the firewall is left in a degraded state.
		logger.L().Error(ctx, "firewall nftables conn reset failed; reusing the old conn",
			zap.String("tap_interface", fw.tapInterface), zap.Error(resetErr))
	case resetErr != nil:
		fw.conn = conn
		logger.L().Error(ctx, "firewall nftables conn reset after apply failure encountered errors",
			zap.String("tap_interface", fw.tapInterface), zap.Error(resetErr))
	default:
		fw.conn = conn
		logger.L().Warn(ctx, "firewall nftables conn reset after apply failure",
			zap.String("tap_interface", fw.tapInterface))
	}

	return resetErr
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

// addEstablishedAcceptRule buffers a rule that accepts ESTABLISHED/RELATED
// return traffic from the tap interface, so response packets are allowed even
// when the source sits in a deny set. Buffer-only; the caller flushes.
func (fw *Firewall) addEstablishedAcceptRule() {
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
}

// addTapDropRule buffers a rule that drops every packet from the tap interface,
// regardless of protocol or destination. Buffer-only; the caller flushes.
func (fw *Firewall) addTapDropRule() {
	fw.conn.AddRule(&nftables.Rule{
		Table: fw.table,
		Chain: fw.filterChain,
		Exprs: append(fw.tapIfaceMatch(),
			&expr.Verdict{Kind: expr.VerdictDrop},
		),
	})
}

// installRules buffers the filter chain rules. When byop is true, Rule 3 drops
// non-TCP only (TCP shifts to the userspace SOCKS5 proxy); otherwise it drops
// all protocols. Buffer-only; the caller must Flush.
func (fw *Firewall) installRules(byop bool) {
	// FILTER CHAIN (PREROUTING, priority -150)
	//   1. ESTABLISHED/RELATED → accept
	//   2. predefinedAllowSet → accept (all protocols)
	//   3. predefinedDenySet → DROP (all protocols, or non-TCP only when byop)
	//   4. Non-TCP: userAllowSet → accept
	//   5. Non-TCP: userDenySet → DROP
	//   6. Default: ACCEPT (TCP handled by iptables REDIRECT in host netns)

	// Rule 1: Allow ESTABLISHED/RELATED connections - all protocols
	// This ensures response packets are allowed even if the source is in predefinedDenySet
	fw.addEstablishedAcceptRule()

	// Rule 2: predefinedAllowSet → accept (all protocols)
	fw.addSetFilterRule(fw.predefinedAllowSet.Set(), false)

	// Rule 3: predefinedDenySet → DROP (all protocols, or non-TCP only in BYOP).
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
}

// bufferUserRules buffers a full replacement of every firewall set.
// Buffer-only; the caller flushes.
func (fw *Firewall) bufferUserRules(allowedCIDRs, deniedCIDRs []string) error {
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

	return nil
}

// ApplyRules reinstalls the filter chain in the given BYOP mode and replaces
// all firewall sets, committed in a single atomic flush so the kernel never
// holds the new Rule 3 mode with stale user sets. The chain is always rebuilt
// from scratch; on flush failure the kernel keeps the previous ruleset and no
// in-memory state can desync from it.
//
// On any failure the conn is replaced via resetConn, so a poisoned batch can
// never leak into a later flush.
func (fw *Firewall) ApplyRules(ctx context.Context, byop bool, allowedCIDRs, deniedCIDRs []string) (err error) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	defer func() {
		if err != nil {
			err = errors.Join(err, fw.resetConn(ctx))
		}
	}()

	fw.conn.FlushChain(fw.filterChain)
	fw.installRules(byop)
	if err := fw.bufferUserRules(allowedCIDRs, deniedCIDRs); err != nil {
		return err
	}

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush atomic rule application: %w", err)
	}

	return nil
}

// DenyEgress rebuilds the filter chain so that every packet originating from
// the guest (the tap interface) is dropped, except ESTABLISHED/RELATED return
// traffic. Unlike the user deny set — which drops only non-TCP traffic and
// leaves TCP to the egress proxy — this drops ALL protocols and allows NO
// guest-initiated destination, so a sandbox isolated this way cannot reach the
// network at all, including the orchestrator's own in-sandbox IP (which also
// fronts the NFS proxy, portmapper and hyperloop). The orchestrator still
// drives the resume because it connects INTO the guest (envd /init, health
// probes) and those replies match ESTABLISHED/RELATED; nothing about the resume
// needs the guest to open a connection outward. It backs the throwaway
// pause-resume prefetch harvest sandbox, whose envd init must not egress — the
// throwaway is also resumed with its volume mounts suppressed so /init does not
// attempt the (now-blocked) NFS mount.
//
// Like ApplyRules the chain is rebuilt from scratch and committed in a single
// flush; on failure the conn is reset so a poisoned batch cannot leak into a
// later flush.
func (fw *Firewall) DenyEgress(ctx context.Context) (err error) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	defer func() {
		if err != nil {
			err = errors.Join(err, fw.resetConn(ctx))
		}
	}()

	fw.conn.FlushChain(fw.filterChain)

	// Rule 1: ESTABLISHED/RELATED → accept (lets the orchestrator-driven control
	// path, which connects into the guest, get its replies back).
	fw.addEstablishedAcceptRule()
	// Rule 2: everything else from the tap → drop, all protocols. No
	// guest-initiated egress is allowed, not even to the orchestrator IP.
	fw.addTapDropRule()

	if err := fw.conn.Flush(); err != nil {
		return fmt.Errorf("flush deny-egress rules: %w", err)
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
