//go:build linux

package host

import (
	"context"
	"fmt"
	"os/exec"
)

// PinMMDSRoute pins a RETURN rule for MMDS traffic (169.254.169.254:80) at
// position 1 of nat PREROUTING and OUTPUT. Idempotent: each run deletes any
// existing copy of the rule first, then re-inserts at position 1, so user
// rules added above ours get pushed down.
//
// Intended for the self-heal path: only called when a real MMDS lookup
// fails. Returns the first -I failure (if any); -D failures are expected
// (rule absent on first run) and silently swallowed.
func PinMMDSRoute(ctx context.Context) error {
	rule := []string{"-d", "169.254.169.254", "-p", "tcp", "--dport", "80", "-j", "RETURN"}
	for _, chain := range []string{"PREROUTING", "OUTPUT"} {
		// -D fails when the rule is absent (exit 1, expected on first run);
		// nothing actionable to log.
		_ = iptables(ctx, append([]string{"-D", chain}, rule...)...)
		if err := iptables(ctx, append([]string{"-I", chain, "1"}, rule...)...); err != nil {
			return fmt.Errorf("iptables -I nat %s: %w", chain, err)
		}
	}

	return nil
}

// iptables runs `iptables -w 5 -t nat ...`. -w waits up to 5s for the
// xtables lock (a user iptables process may race us).
func iptables(ctx context.Context, args ...string) error {
	full := append([]string{"-w", "5", "-t", "nat"}, args...)
	out, err := exec.CommandContext(ctx, "iptables", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}

	return nil
}
