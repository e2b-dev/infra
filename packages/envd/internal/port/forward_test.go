package port

import (
	"context"
	"syscall"
	"testing"
	"time"

	gopsnet "github.com/shirou/gopsutil/v4/net"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
)

// newTestForwarder creates a Forwarder suitable for unit tests.  It uses a
// NoopManager so startPortForwarding can be called without panicking (socat
// itself may not be installed and will simply fail to start, which is fine —
// the ports map entry is inserted before the exec attempt).
func newTestForwarder(scanner *Scanner) *Forwarder {
	l := zerolog.Nop()

	return &Forwarder{
		logger:            &l,
		ports:             make(map[string]*PortToForward),
		sourceIP:          defaultGatewayIP,
		scannerSubscriber: scanner.AddSubscriber("test", nil),
		cgroupManager:     cgroups.NewNoopManager(),
	}
}

// TestStartForwarding_StopsOnClosedMessages pins the defensive guard on the
// scan-result receive. Nothing closes Messages today, but a one-value receive
// would degrade badly if that ever changed: a closed channel is permanently
// ready, so the loop would spin on nil procs and stop every socat on each pass.
// The two-value receive turns that into a clean stop.
func TestStartForwarding_StopsOnClosedMessages(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(time.Hour)
	f := newTestForwarder(scanner)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		f.StartForwarding(context.Background())
	}()

	// Nothing is scanning (period is an hour and ScanAndBroadcast is never
	// started), so this close cannot race a Signal.
	close(f.scannerSubscriber.Messages)

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("StartForwarding did not stop after Messages was closed")
	}
}

// TestStartForwarding_WildcardIPv6_NormalizedToFamilyFour verifies that a "::"
// wildcard listener is assigned family=4 so socat connects via 127.0.0.1 (Fix A
// normalization + Fix C). On a dual-stack kernel, frameworks like gRPC bind "::"
// by default; routing them through IPv4 avoids /etc/hosts resolution of "::1
// localhost" on minimal images.
func TestStartForwarding_WildcardIPv6_NormalizedToFamilyFour(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(time.Hour)
	f := newTestForwarder(scanner)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		f.StartForwarding(context.Background())
	}()

	f.scannerSubscriber.Messages <- []gopsnet.ConnectionStat{
		{Pid: 42, Family: syscall.AF_INET6, Status: "LISTEN",
			Laddr: gopsnet.Addr{IP: "::", Port: 8080}},
	}
	close(f.scannerSubscriber.Messages)

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("StartForwarding did not stop")
	}

	// The goroutine has exited so f.ports is safe to read without the lock.
	key := "42-8080-::"
	ptf, ok := f.ports[key]
	if !ok {
		t.Fatalf("expected ports[%q] but got keys %v", key, portKeys(f.ports))
	}
	if ptf.family != 4 {
		t.Errorf("family = %d, want 4 (wildcard :: must be normalized to IPv4)", ptf.family)
	}
}

// TestStartForwarding_DualStackKey_TwoEntries verifies that a service listening
// on both 127.0.0.1:PORT and ::1:PORT receives two independent port-forward
// entries (Fix B). Before the fix the key omitted the IP, so the second entry
// overwrote the first and only one socat was started.
func TestStartForwarding_DualStackKey_TwoEntries(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(time.Hour)
	f := newTestForwarder(scanner)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		f.StartForwarding(context.Background())
	}()

	f.scannerSubscriber.Messages <- []gopsnet.ConnectionStat{
		{Pid: 100, Family: syscall.AF_INET, Status: "LISTEN",
			Laddr: gopsnet.Addr{IP: "127.0.0.1", Port: 9090}},
		{Pid: 100, Family: syscall.AF_INET6, Status: "LISTEN",
			Laddr: gopsnet.Addr{IP: "::1", Port: 9090}},
	}
	close(f.scannerSubscriber.Messages)

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("StartForwarding did not stop")
	}

	wantKeys := []string{"100-9090-127.0.0.1", "100-9090-::1"}
	for _, k := range wantKeys {
		if _, ok := f.ports[k]; !ok {
			t.Errorf("expected ports[%q] but got keys %v", k, portKeys(f.ports))
		}
	}
	if len(f.ports) != 2 {
		t.Errorf("len(ports) = %d, want 2; keys: %v", len(f.ports), portKeys(f.ports))
	}
}

func portKeys(m map[string]*PortToForward) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
