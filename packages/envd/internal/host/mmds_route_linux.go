//go:build linux

package host

import (
	"context"
	"os/exec"
)

// PinMMDSRoute pins a RETURN rule for MMDS traffic (169.254.169.254:80) at
// position 1 of nat PREROUTING and OUTPUT. Idempotent: each run deletes any
// existing copy of the rule first, then re-inserts at position 1, so user
// rules added above ours get pushed down.
//
// Intended for the self-heal path: only called when a real MMDS lookup
// fails, on the assumption that user iptables in the same netns clobbered
// our route.
func PinMMDSRoute(ctx context.Context) {
	rule := []string{"-d", "169.254.169.254", "-p", "tcp", "--dport", "80", "-j", "RETURN"}
	for _, chain := range []string{"PREROUTING", "OUTPUT"} {
		// -D fails when the rule is absent; expected on first run. Swallow.
		run(ctx, append([]string{"-D", chain}, rule...)...)
		run(ctx, append([]string{"-I", chain, "1"}, rule...)...)
	}
}

// run executes iptables in the nat table with -w to wait for the xtables
// lock (a user iptables process may race us). Errors are intentionally
// swallowed; this is best-effort self-heal.
func run(ctx context.Context, args ...string) {
	full := append([]string{"-w", "5", "-t", "nat"}, args...)
	_ = exec.CommandContext(ctx, "iptables", full...).Run()
}
