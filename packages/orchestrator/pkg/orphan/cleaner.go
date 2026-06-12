//go:build linux

package orphan

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/vishvananda/netlink"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// sigTermGracePeriod is how long we wait after SIGTERM before sending SIGKILL.
	sigTermGracePeriod = 15 * time.Second
)

// CleanResult summarises what was actually removed during a cleanup pass.
type CleanResult struct {
	KilledPIDs      []int32
	RemovedSockets  []string
	RemovedFIFOs    []string
	RemovedVeths    []string
	Errors          []error
}

// cleanOrphanedProcesses sends SIGTERM to each orphaned process, waits up to
// sigTermGracePeriod, then sends SIGKILL if the process is still alive.
// It is safe to call with an empty slice.
func cleanOrphanedProcesses(ctx context.Context, orphans []OrphanedProcess) CleanResult {
	var result CleanResult

	for _, o := range orphans {
		if err := killProcess(ctx, o.PID); err != nil {
			logger.L().Error(ctx, "orphan cleaner: failed to kill process",
				zap.Int32("pid", o.PID),
				zap.String("socket", o.SocketPath),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, fmt.Errorf("kill pid %d: %w", o.PID, err))

			continue
		}

		logger.L().Info(ctx, "orphan cleaner: killed orphaned firecracker process",
			zap.Int32("pid", o.PID),
			zap.Int32("ppid", o.PPID),
			zap.String("socket", o.SocketPath),
		)

		result.KilledPIDs = append(result.KilledPIDs, o.PID)
	}

	return result
}

// killProcess sends SIGTERM to pid, waits sigTermGracePeriod, then sends
// SIGKILL if the process is still running.
func killProcess(ctx context.Context, pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		// Process already gone — treat as success.
		return nil
	}

	// Send SIGTERM.
	if err := p.SendSignal(syscall.SIGTERM); err != nil {
		// ESRCH means the process already exited.
		if isNoSuchProcess(err) {
			return nil
		}

		return fmt.Errorf("SIGTERM: %w", err)
	}

	logger.L().Info(ctx, "orphan cleaner: sent SIGTERM, waiting for graceful exit",
		zap.Int32("pid", pid),
		zap.Duration("grace_period", sigTermGracePeriod),
	)

	// Poll until the process exits or the grace period expires.
	deadline := time.Now().Add(sigTermGracePeriod)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}

		running, err := p.IsRunning()
		if err != nil || !running {
			return nil
		}
	}

	// Grace period expired — escalate to SIGKILL.
	logger.L().Warn(ctx, "orphan cleaner: grace period expired, sending SIGKILL",
		zap.Int32("pid", pid),
	)

	if err := p.SendSignal(syscall.SIGKILL); err != nil {
		if isNoSuchProcess(err) {
			return nil
		}

		return fmt.Errorf("SIGKILL: %w", err)
	}

	return nil
}

// isNoSuchProcess returns true for errors that indicate the target process no
// longer exists (ESRCH or "no such process").
func isNoSuchProcess(err error) bool {
	if err == nil {
		return false
	}

	return err == syscall.ESRCH || err.Error() == "no such process"
}

// cleanOrphanedSockets removes socket files from disk.
func cleanOrphanedSockets(ctx context.Context, orphans []OrphanedSocket) CleanResult {
	var result CleanResult

	for _, o := range orphans {
		if err := os.Remove(o.Path); err != nil && !os.IsNotExist(err) {
			logger.L().Error(ctx, "orphan cleaner: failed to remove socket",
				zap.String("path", o.Path),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, fmt.Errorf("remove socket %s: %w", o.Path, err))

			continue
		}

		logger.L().Info(ctx, "orphan cleaner: removed orphaned socket", zap.String("path", o.Path))
		result.RemovedSockets = append(result.RemovedSockets, o.Path)
	}

	return result
}

// cleanOrphanedFIFOs removes FIFO files from disk.
func cleanOrphanedFIFOs(ctx context.Context, orphans []OrphanedFIFO) CleanResult {
	var result CleanResult

	for _, o := range orphans {
		if err := os.Remove(o.Path); err != nil && !os.IsNotExist(err) {
			logger.L().Error(ctx, "orphan cleaner: failed to remove FIFO",
				zap.String("path", o.Path),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, fmt.Errorf("remove fifo %s: %w", o.Path, err))

			continue
		}

		logger.L().Info(ctx, "orphan cleaner: removed orphaned FIFO", zap.String("path", o.Path))
		result.RemovedFIFOs = append(result.RemovedFIFOs, o.Path)
	}

	return result
}

// cleanOrphanedVeths removes orphaned veth interfaces and their associated
// iptables FORWARD / POSTROUTING / PREROUTING rules.
//
// The iptables deletions are best-effort: if a rule does not exist the error is
// silently ignored so that the function remains idempotent.
func cleanOrphanedVeths(ctx context.Context, orphans []OrphanedVeth) CleanResult {
	var result CleanResult

	if len(orphans) == 0 {
		return result
	}

	tables, err := iptables.New()
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("init iptables: %w", err))
		// Still attempt netlink deletions below.
	}

	for _, o := range orphans {
		if tables != nil {
			cleanVethIPTables(ctx, tables, o.Name, &result)
		}

		// Delete the veth interface itself.
		link, err := netlink.LinkByName(o.Name)
		if err != nil {
			// Interface already gone — not an error.
			logger.L().Info(ctx, "orphan cleaner: veth already absent",
				zap.String("veth", o.Name),
			)
		} else {
			if err := netlink.LinkDel(link); err != nil {
				logger.L().Error(ctx, "orphan cleaner: failed to delete veth",
					zap.String("veth", o.Name),
					zap.Error(err),
				)
				result.Errors = append(result.Errors, fmt.Errorf("delete veth %s: %w", o.Name, err))

				continue
			}

			logger.L().Info(ctx, "orphan cleaner: deleted orphaned veth",
				zap.String("veth", o.Name),
				zap.Int("slot_idx", o.SlotIdx),
			)
		}

		result.RemovedVeths = append(result.RemovedVeths, o.Name)
	}

	return result
}

// cleanVethIPTables removes all iptables rules that reference vethName.
// Errors from rules that do not exist are silently ignored (idempotent).
func cleanVethIPTables(ctx context.Context, tables *iptables.IPTables, vethName string, result *CleanResult) {
	type rule struct {
		table string
		chain string
		args  []string
	}

	// We delete the two FORWARD rules and the POSTROUTING MASQUERADE rule that
	// CreateNetwork adds for every slot.  We do not know the exact CIDR here so
	// we use iptables list + grep to find and delete matching rules.
	rules := []rule{
		{
			"filter", "FORWARD",
			[]string{"-i", vethName, "-j", "ACCEPT"},
		},
		{
			"filter", "FORWARD",
			[]string{"-o", vethName, "-j", "ACCEPT"},
		},
	}

	for _, r := range rules {
		if err := tables.DeleteIfExists(r.table, r.chain, r.args...); err != nil {
			logger.L().Error(ctx, "orphan cleaner: failed to delete iptables rule",
				zap.String("veth", vethName),
				zap.String("table", r.table),
				zap.String("chain", r.chain),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, fmt.Errorf("iptables %s/%s %s: %w", r.table, r.chain, vethName, err))
		}
	}

	// Remove all PREROUTING rules that reference this veth by scanning the
	// full rule list and deleting matching entries.
	cleanChainByInterface(ctx, tables, "nat", "PREROUTING", vethName, result)
	cleanChainByInterface(ctx, tables, "nat", "POSTROUTING", vethName, result)
}

// cleanChainByInterface lists all rules in table/chain and deletes any that
// contain vethName as an interface specifier (-i or -o or --in-interface or
// --out-interface).
func cleanChainByInterface(ctx context.Context, tables *iptables.IPTables, table, chain, vethName string, result *CleanResult) {
	rules, err := tables.List(table, chain)
	if err != nil {
		// Chain may not exist on this host.
		return
	}

	for _, rule := range rules {
		if !containsInterface(rule, vethName) {
			continue
		}

		// Parse the rule back into individual arguments by splitting on spaces.
		// iptables.List returns rules in the form "-A CHAIN <args...>".
		args := parseIPTablesRule(rule, chain)
		if len(args) == 0 {
			continue
		}

		if err := tables.DeleteIfExists(table, chain, args...); err != nil {
			logger.L().Error(ctx, "orphan cleaner: failed to delete iptables rule",
				zap.String("veth", vethName),
				zap.String("rule", rule),
				zap.Error(err),
			)
			result.Errors = append(result.Errors, fmt.Errorf("iptables delete %s/%s: %w", table, chain, err))
		} else {
			logger.L().Info(ctx, "orphan cleaner: deleted iptables rule",
				zap.String("veth", vethName),
				zap.String("table", table),
				zap.String("chain", chain),
				zap.String("rule", rule),
			)
		}
	}
}

// containsInterface returns true if the iptables rule string references iface
// as an -i / -o / --in-interface / --out-interface value.
func containsInterface(rule, iface string) bool {
	for _, flag := range []string{"-i " + iface, "-o " + iface, "--in-interface " + iface, "--out-interface " + iface} {
		if len(rule) >= len(flag) {
			for i := 0; i <= len(rule)-len(flag); i++ {
				if rule[i:i+len(flag)] == flag {
					return true
				}
			}
		}
	}

	return false
}

// parseIPTablesRule strips the leading "-A <chain>" prefix from a rule string
// returned by iptables.List and returns the remaining arguments as a slice.
func parseIPTablesRule(rule, chain string) []string {
	prefix := "-A " + chain + " "
	if len(rule) <= len(prefix) {
		return nil
	}

	rest := rule[len(prefix):]

	// Simple whitespace split — sufficient for the flag values we generate.
	var args []string
	var cur []byte
	inQuote := false

	for i := 0; i < len(rest); i++ {
		c := rest[i]

		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			if len(cur) > 0 {
				args = append(args, string(cur))
				cur = cur[:0]
			}
		default:
			cur = append(cur, c)
		}
	}

	if len(cur) > 0 {
		args = append(args, string(cur))
	}

	return args
}
