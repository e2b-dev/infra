//go:build linux

package host

import (
	"context"
	"os/exec"
)

// PinMMDSRoute installs a private nat chain (E2B_MMDS) that returns early
// for MMDS traffic (169.254.169.254:80) and pins the jump at position 1 in
// PREROUTING and OUTPUT, removing any prior copies first. Idempotent.
//
// Intended for the self-heal path: only called when a real MMDS lookup
// fails, on the assumption that user iptables in the same netns clobbered
// our route. Re-running puts our jump back at the front of both chains.
func PinMMDSRoute(ctx context.Context) {
	commands := [][]string{
		{"-N", "E2B_MMDS"},
		{"-F", "E2B_MMDS"},
		{"-A", "E2B_MMDS", "-d", "169.254.169.254", "-p", "tcp", "--dport", "80", "-j", "RETURN"},
		{"-D", "PREROUTING", "-j", "E2B_MMDS"},
		{"-I", "PREROUTING", "1", "-j", "E2B_MMDS"},
		{"-D", "OUTPUT", "-j", "E2B_MMDS"},
		{"-I", "OUTPUT", "1", "-j", "E2B_MMDS"},
	}
	for _, args := range commands {
		// -N fails when chain exists; -D fails when jump is absent. Both
		// are expected on the first / clean-state run; swallow errors.
		_ = exec.CommandContext(ctx, "iptables", append([]string{"-t", "nat"}, args...)...).Run()
	}
}
