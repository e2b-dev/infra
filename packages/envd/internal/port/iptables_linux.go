//go:build linux

package port

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// iptablesDNAT redirects sourceIP:port → 127.0.0.1:port via PREROUTING.
// Requires net.ipv4.conf.<iface>.route_localnet=1 (set in setupIPv4DNAT).
type iptablesBackend struct {
	sourceIP string
}

func newIPtablesBackend(sourceIP string) *iptablesBackend {
	return &iptablesBackend{sourceIP: sourceIP}
}

// setupIPv4DNAT enables route_localnet so DNAT-to-127.0.0.1 works.
func setupIPv4DNAT() error {
	const p = "/proc/sys/net/ipv4/conf/all/route_localnet"

	return os.WriteFile(p, []byte("1\n"), 0o644)
}

func (b *iptablesBackend) addRule(ctx context.Context, port uint32) error {
	// -C first: avoids duplicate rules across envd restarts (previous rules
	// outlive the process; the internal `ports` map does not).
	if err := b.runRule(ctx, "-C", port); err == nil {
		return nil
	}

	return b.runRule(ctx, "-A", port)
}

func (b *iptablesBackend) deleteRule(ctx context.Context, port uint32) error {
	return b.runRule(ctx, "-D", port)
}

func (b *iptablesBackend) runRule(ctx context.Context, op string, port uint32) error {
	out, err := exec.CommandContext(ctx,
		"iptables", "-t", "nat", op, "PREROUTING",
		"-d", b.sourceIP,
		"-p", "tcp", "--dport", fmt.Sprintf("%d", port),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("127.0.0.1:%d", port),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables %s :%d: %w (%s)", op, port, err, string(out))
	}

	return nil
}
