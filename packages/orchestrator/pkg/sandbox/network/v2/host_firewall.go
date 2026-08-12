//go:build linux

package v2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// HostFirewall is a singleton that manages the host-level nftables table
// "v2-host-firewall". It uses sets so rule count stays constant regardless
// of sandbox count — lookups are O(1) hash operations.
//
// Table layout:
//
//	table inet v2-host-firewall {
//	  set v2_veths      { type ifname; }
//	  set v2_host_cidrs { type ipv4_addr; flags interval; }
//
//	  chain forward { type filter hook forward priority 0; policy accept;
//	    iifname @v2_veths oifname <gw> accept
//	    iifname <gw> oifname @v2_veths accept
//	  }
//	  chain prerouting { type nat hook prerouting priority -100;
//	    # service redirects (all slots share same ports)
//	    iifname @v2_veths tcp dport 80  ip daddr <orchIP> redirect to :hyperloopPort
//	    iifname @v2_veths tcp dport 111 ip daddr <orchIP> redirect to :portmapperPort
//	    iifname @v2_veths tcp dport 2049 ip daddr <orchIP> redirect to :nfsPort
//	    # TCP firewall proxy redirects
//	    iifname @v2_veths tcp dport 80  redirect to :tcpHTTPPort
//	    iifname @v2_veths tcp dport 443 redirect to :tcpTLSPort
//	    iifname @v2_veths tcp redirect to :tcpOtherPort   # every remaining TCP
//	  }
//	  chain postrouting { type nat hook postrouting priority 100;
//	    ip saddr @v2_host_cidrs oifname <gw> masquerade
//	  }
//	}
//
// All slots share the same redirect ports, so only per-slot data (veth names
// and host CIDRs) goes into sets and the rule count stays constant.
type HostFirewall struct {
	conn  *nftables.Conn
	table *nftables.Table

	vethSet *nftables.Set // type ifname; elements = veth interface names
	cidrSet *nftables.Set // type ipv4_addr; flags interval; elements = host CIDRs

	defaultGw string
	config    network.Config
	mu        sync.Mutex
}

const (
	hostFwTableName = "v2-host-firewall"
)

// NewHostFirewall creates or opens the singleton host firewall table with all
// required sets and chains. Call once per orchestrator process.
//
// Restart-safe: if the table already exists from a previous run (e.g., orchestrator
// restart with live sandboxes), existing set elements are preserved. Only chain rules
// are refreshed with current config. This prevents connectivity loss for sandboxes
// that survived the restart.
func NewHostFirewall(defaultGw string, config network.Config) (_ *HostFirewall, err error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("new nftables conn: %w", err)
	}

	defer func() {
		if err != nil {
			err = errors.Join(err, conn.CloseLasting())
		}
	}()

	// Ensure table exists — idempotent: creates if new, opens if existing.
	// We do NOT delete the table on startup. Existing set elements represent
	// live sandboxes whose connectivity must be preserved across restarts.
	table := conn.AddTable(&nftables.Table{
		Name:   hostFwTableName,
		Family: nftables.TableFamilyINet,
	})

	hf := &HostFirewall{
		conn:      conn,
		table:     table,
		defaultGw: defaultGw,
		config:    config,
	}

	if err := hf.ensureSets(); err != nil {
		return nil, fmt.Errorf("ensure sets: %w", err)
	}

	if err := hf.ensureChains(); err != nil {
		return nil, fmt.Errorf("ensure chains: %w", err)
	}

	return hf, nil
}

// ensureSets creates sets if they don't exist, or opens handles to existing ones.
// Existing set elements (active sandbox veth names and host CIDRs) are preserved.
func (hf *HostFirewall) ensureSets() error {
	// Set of veth interface names — AddSet is idempotent for existing sets
	hf.vethSet = &nftables.Set{
		Table:   hf.table,
		Name:    "v2_veths",
		KeyType: nftables.TypeIFName,
	}
	if err := hf.conn.AddSet(hf.vethSet, nil); err != nil {
		return fmt.Errorf("add veth set: %w", err)
	}

	// Set of host CIDRs — interval set for /32 entries
	hf.cidrSet = &nftables.Set{
		Table:    hf.table,
		Name:     "v2_host_cidrs",
		KeyType:  nftables.TypeIPAddr,
		Interval: true,
	}
	if err := hf.conn.AddSet(hf.cidrSet, nil); err != nil {
		return fmt.Errorf("add cidr set: %w", err)
	}

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush sets: %w", err)
	}

	return nil
}

// ensureChains creates or refreshes all chains with current config.
// Chains are flushed and re-populated — this is safe because chain rules are
// config-dependent (ports, gateway interface), not state-dependent.
// Set elements (which represent live sandboxes) are NOT touched.
// The entire operation is atomic via nftables batching.
func (hf *HostFirewall) ensureChains() error {
	gwBytes := ifnameBytes(hf.defaultGw)
	orchIP := net.ParseIP(hf.config.OrchestratorInSandboxIPAddress).To4()

	// --- FORWARD chain ---
	// Accept policy, matching v1: the slot rules below only add ACCEPTs, so
	// this chain stays transparent to every other forwarding path on the host.
	fwdPolicy := nftables.ChainPolicyAccept
	fwdChain := hf.conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    hf.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &fwdPolicy,
	})
	hf.conn.FlushChain(fwdChain) // clear stale rules from previous config

	// iifname @v2_veths oifname <gw> accept
	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: fwdChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Lookup{SourceRegister: 1, SetName: hf.vethSet.Name, SetID: hf.vethSet.ID},
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: gwBytes},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	})

	// iifname <gw> oifname @v2_veths accept — unconditional, as v1's FORWARD
	// rule is; a conntrack-state match here would break new inbound flows v1
	// allows.
	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: fwdChain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: gwBytes},
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Lookup{SourceRegister: 1, SetName: hf.vethSet.Name, SetID: hf.vethSet.ID},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	})

	// --- PREROUTING chain (NAT) ---
	preChain := hf.conn.AddChain(&nftables.Chain{
		Name:     "prerouting",
		Table:    hf.table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(-100),
	})
	hf.conn.FlushChain(preChain)

	// Service redirects: iifname @v2_veths + ip daddr <orchIP> + tcp dport X → redirect to :port
	svcRedirects := []struct {
		dport uint16
		rport uint16
	}{
		{80, hf.config.HyperloopProxyPort},
		{111, hf.config.PortmapperPort},
		{2049, hf.config.NFSProxyPort},
	}
	for _, svc := range svcRedirects {
		hf.conn.AddRule(&nftables.Rule{
			Table: hf.table,
			Chain: preChain,
			Exprs: svcRedirectExprs(hf.vethSet, orchIP, svc.dport, svc.rport),
		})
	}

	// TCP firewall proxy redirects:
	// Port 80 (non-service, i.e., daddr != orchIP) → tcpHTTPPort
	// Port 443 → tcpTLSPort
	// All other TCP → tcpOtherPort
	tcpRedirects := []struct {
		dport uint16
		rport uint16
	}{
		{80, hf.config.SandboxTCPFirewallHTTPPort},
		{443, hf.config.SandboxTCPFirewallTLSPort},
	}
	for _, tcp := range tcpRedirects {
		hf.conn.AddRule(&nftables.Rule{
			Table: hf.table,
			Chain: preChain,
			Exprs: tcpRedirectExprs(hf.vethSet, tcp.dport, tcp.rport),
		})
	}

	// Catch-all TCP redirect: any remaining TCP from v2 veths → tcpOtherPort
	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: preChain,
		Exprs: tcpCatchAllExprs(hf.vethSet, hf.config.SandboxTCPFirewallOtherPort),
	})

	// --- POSTROUTING chain (NAT) ---
	postChain := hf.conn.AddChain(&nftables.Chain{
		Name:     "postrouting",
		Table:    hf.table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
	})
	hf.conn.FlushChain(postChain)

	// ip saddr @v2_host_cidrs oifname <gw> masquerade
	hf.conn.AddRule(&nftables.Rule{
		Table: hf.table,
		Chain: postChain,
		Exprs: []expr.Any{
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
			&expr.Lookup{SourceRegister: 1, SetName: hf.cidrSet.Name, SetID: hf.cidrSet.ID},
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: gwBytes},
			&expr.Masq{},
		},
	})

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush chains: %w", err)
	}

	return nil
}

// classifyVeths splits the set's current elements into those no live slot
// owns and the live veths the set is missing. Pure, so the padded-key
// comparison it turns on can be pinned by a unit test.
func classifyVeths(current []nftables.SetElement, desired map[string]bool) (stale []nftables.SetElement, missing []string) {
	present := make(map[string]bool, len(current))
	for _, elem := range current {
		name := ifnameString(elem.Key)
		present[name] = true
		if !desired[name] {
			stale = append(stale, elem)
		}
	}

	for name := range desired {
		if !present[name] {
			missing = append(missing, name)
		}
	}

	slices.Sort(missing)

	return stale, missing
}

// ReconcileSlots reconciles host firewall set membership with actual active slots.
// Call on startup after rebuilding the in-memory slot registry from surviving sandboxes.
// - Slots in the registry but missing from sets are added.
// - Set entries not in the registry are removed (stale leftovers from crashed sandboxes).
func (hf *HostFirewall) ReconcileSlots(ctx context.Context, activeSlots []*SlotV2) (err error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	defer hf.resetConnOnError(ctx, &err)

	// Build desired state from active slots
	desiredVeths := make(map[string]bool)
	desiredCIDRs := make(map[string]bool) // key = hostIP string
	for _, sv2 := range activeSlots {
		desiredVeths[sv2.Slot.VethName()] = true
		desiredCIDRs[sv2.Slot.HostIP.To4().String()] = true
	}

	// Read current veth set elements
	currentVeths, err := hf.conn.GetSetElements(hf.vethSet)
	if err != nil {
		return fmt.Errorf("get veth set elements: %w", err)
	}

	stale, missing := classifyVeths(currentVeths, desiredVeths)
	if len(stale) > 0 {
		hf.conn.SetDeleteElements(hf.vethSet, stale)
	}
	for _, veth := range missing {
		if err := hf.conn.SetAddElements(hf.vethSet, []nftables.SetElement{
			{Key: ifnameBytes(veth)},
		}); err != nil {
			return fmt.Errorf("add missing veth %s: %w", veth, err)
		}
	}

	// Reconcile CIDR set: flush and rebuild (interval sets don't support
	// element-level comparison easily, so rebuild is simpler and correct)
	hf.conn.FlushSet(hf.cidrSet)
	for _, sv2 := range activeSlots {
		hostIP := sv2.Slot.HostIP.To4()
		nextIP := incrementIP(hostIP)
		if err := hf.conn.SetAddElements(hf.cidrSet, []nftables.SetElement{
			{Key: hostIP},
			{Key: nextIP, IntervalEnd: true},
		}); err != nil {
			return fmt.Errorf("add host cidr for slot %d: %w", sv2.Slot.Idx, err)
		}
	}

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush reconcile: %w", err)
	}

	return nil
}

// AddSlot adds the veth name and host CIDR for a v2 slot.
func (hf *HostFirewall) AddSlot(ctx context.Context, slotV2 *SlotV2) (err error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	defer hf.resetConnOnError(ctx, &err)

	slot := slotV2.Slot

	// Add veth name to v2_veths set
	if err := hf.conn.SetAddElements(hf.vethSet, []nftables.SetElement{
		{Key: ifnameBytes(slot.VethName())},
	}); err != nil {
		return fmt.Errorf("add veth to set: %w", err)
	}

	// Add host CIDR to v2_host_cidrs interval set (/32)
	hostIP := slot.HostIP.To4()
	nextIP := incrementIP(hostIP)
	if err := hf.conn.SetAddElements(hf.cidrSet, []nftables.SetElement{
		{Key: hostIP},
		{Key: nextIP, IntervalEnd: true},
	}); err != nil {
		return fmt.Errorf("add host cidr to set: %w", err)
	}

	if err := hf.conn.Flush(); err != nil {
		return fmt.Errorf("flush add slot: %w", err)
	}

	return nil
}

// RemoveSlot removes the veth name and host CIDR for a v2 slot.
func (hf *HostFirewall) RemoveSlot(ctx context.Context, slotV2 *SlotV2) (err error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	defer hf.resetConnOnError(ctx, &err)

	slot := slotV2.Slot

	hf.conn.SetDeleteElements(hf.vethSet, []nftables.SetElement{
		{Key: ifnameBytes(slot.VethName())},
	})

	hostIP := slot.HostIP.To4()
	nextIP := incrementIP(hostIP)
	hf.conn.SetDeleteElements(hf.cidrSet, []nftables.SetElement{
		{Key: hostIP},
		{Key: nextIP, IntervalEnd: true},
	})

	// Already-absent elements make this a no-op, not a failure: teardown has
	// to be retryable after a partial create.
	if err := hf.conn.Flush(); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("flush remove slot: %w", err)
	}

	return nil
}

// Close releases the netlink connection, and deletes the table only when no
// slot is left in it. Sandboxes outlive the orchestrator process: deleting the
// table while their veths are still registered would cut their connectivity
// until the next start rebuilt it.
func (hf *HostFirewall) Close() error {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	elements, err := hf.conn.GetSetElements(hf.vethSet)
	if err != nil {
		return errors.Join(fmt.Errorf("get veth set elements: %w", err), hf.conn.CloseLasting())
	}

	if len(elements) == 0 {
		hf.conn.DelTable(hf.table)
		if err := hf.conn.Flush(); err != nil {
			return errors.Join(fmt.Errorf("delete host firewall table: %w", err), hf.conn.CloseLasting())
		}
	}

	return hf.conn.CloseLasting()
}

// --- nftables expression builders ---

// svcRedirectExprs builds: iifname @set tcp dport X ip daddr <orchIP> redirect to :port
func svcRedirectExprs(vethSet *nftables.Set, orchIP net.IP, dport, rport uint16) []expr.Any {
	return []expr.Any{
		// Match TCP
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: []byte{unix.IPPROTO_TCP}},
		// iifname @v2_veths
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: vethSet.Name, SetID: vethSet.ID},
		// ip daddr == orchIP
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: orchIP},
		// tcp dport == dport
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: portBytes(dport)},
		// redirect to :rport
		&expr.Immediate{Register: 1, Data: portBytes(rport)},
		&expr.Redir{RegisterProtoMin: 1},
	}
}

// tcpRedirectExprs builds: iifname @set tcp dport X redirect to :port
func tcpRedirectExprs(vethSet *nftables.Set, dport, rport uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: vethSet.Name, SetID: vethSet.ID},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: portBytes(dport)},
		&expr.Immediate{Register: 1, Data: portBytes(rport)},
		&expr.Redir{RegisterProtoMin: 1},
	}
}

// tcpCatchAllExprs builds: iifname @set tcp protocol redirect to :port
func tcpCatchAllExprs(vethSet *nftables.Set, rport uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Register: 1, Op: expr.CmpOpEq, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: vethSet.Name, SetID: vethSet.ID},
		&expr.Immediate{Register: 1, Data: portBytes(rport)},
		&expr.Redir{RegisterProtoMin: 1},
	}
}

// --- helpers ---

// ifnameString reads a set key back as an interface name, dropping the NUL
// padding nftables stores it with.
func ifnameString(key []byte) string {
	return string(bytes.TrimRight(key, "\x00"))
}

// resetConnOnError replaces the shared conn whenever a mutation fails. A
// serialization error sticks to the conn for its lifetime, and a mutation that
// returns before reaching Flush leaves its messages queued for whoever flushes
// next; nftables.Conn exposes no way to clear either. Every slot on the host
// shares this conn, so one failure would otherwise follow every later slot.
func (hf *HostFirewall) resetConnOnError(ctx context.Context, err *error) {
	if *err == nil {
		return
	}

	*err = errors.Join(*err, hf.resetConn(ctx))
}

func (hf *HostFirewall) resetConn(ctx context.Context) error {
	closeErr := hf.conn.CloseLasting()

	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		err = fmt.Errorf("open new lasting nftables conn: %w", err)

		var transientErr error
		conn, transientErr = nftables.New()
		if transientErr != nil {
			err = errors.Join(err, fmt.Errorf("open transient nftables conn: %w", transientErr))
		}
	}

	resetErr := errors.Join(closeErr, err)
	if conn == nil {
		// Keep the old (closed, poisoned) conn rather than storing nil and
		// panicking on next use; the firewall is left degraded.
		logger.L().Error(ctx, "host firewall nftables conn reset failed; reusing the old conn", zap.Error(resetErr))

		return resetErr
	}

	hf.conn = conn
	if resetErr != nil {
		logger.L().Error(ctx, "host firewall nftables conn reset encountered errors", zap.Error(resetErr))
	} else {
		logger.L().Warn(ctx, "host firewall nftables conn reset after a failed mutation")
	}

	return resetErr
}

func portBytes(port uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, port)

	return b
}

func incrementIP(ip net.IP) net.IP {
	result := make(net.IP, len(ip))
	copy(result, ip)
	for i := range slices.Backward(result) {
		result[i]++
		if result[i] != 0 {
			break
		}
	}

	return result
}
