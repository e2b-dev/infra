package tcpfirewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func dial(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{}

	return d.DialContext(ctx, "tcp", addr)
}

func getFreePort(t *testing.T) uint16 {
	t.Helper()
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	return uint16(port)
}

func waitForPort(ctx context.Context, port uint16) error {
	addr := net.JoinHostPort("127.0.0.1", portStr(port))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := dial(ctx, addr)
		if err == nil {
			conn.Close()

			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func portStr(n uint16) string {
	return fmt.Sprintf("%d", n)
}

func TestProxy_StartsAndAcceptsConnections(t *testing.T) {
	t.Parallel()

	httpPort := getFreePort(t)
	tlsPort := getFreePort(t)
	otherPort := getFreePort(t)

	networkConfig := network.Config{
		SandboxTCPFirewallHTTPPort:  httpPort,
		SandboxTCPFirewallTLSPort:   tlsPort,
		SandboxTCPFirewallOtherPort: otherPort,
	}

	sandboxes := sandbox.NewSandboxesMap()
	meterProvider := noop.NewMeterProvider()

	proxy := New(logger.NewNopLogger(), networkConfig, sandboxes, meterProvider)

	// Use separate contexts for proxy and connections
	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	defer proxyCancel()

	connCtx, connCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer connCancel()

	// Start the proxy in a goroutine
	proxyErr := make(chan error, 1)
	go func() {
		proxyErr <- proxy.Start(proxyCtx)
	}()

	// Wait for the proxy to be ready by trying to connect
	require.NoError(t, waitForPort(connCtx, otherPort), "Proxy did not start in time")

	// Connect to each port
	for _, port := range []uint16{httpPort, tlsPort, otherPort} {
		conn, err := dial(connCtx, net.JoinHostPort("127.0.0.1", portStr(port)))
		require.NoError(t, err, "Failed to connect to port %d", port)
		conn.Close()
	}

	// Stop the proxy
	proxyCancel()

	select {
	case err := <-proxyErr:
		// context.Canceled is expected when we cancel the proxy
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Proxy did not stop in time")
	}
}

// TestProxy_RealFDExhaustion tests the proxy with actual file descriptor exhaustion.
// This test lowers the FD limit and opens many connections to trigger real EMFILE errors.
// It verifies that the resilientListener properly recovers when FDs become available.
//
//nolint:paralleltest // This test modifies process-wide resource limits
func TestProxy_RealFDExhaustion(t *testing.T) {
	// Skip in short mode as this test is slower
	if testing.Short() {
		t.Skip("Skipping FD exhaustion test in short mode")
	}

	// Get current limits
	var originalLimit syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &originalLimit)
	require.NoError(t, err, "Failed to get RLIMIT_NOFILE")

	// We need enough FDs for:
	// - 3 listener sockets (proxy)
	// - Some FDs for the test framework, logging, etc. (~20)
	// - FDs we'll open to exhaust the limit
	// Set a low limit to make exhaustion feasible
	const lowLimit = 64
	newLimit := syscall.Rlimit{
		Cur: lowLimit,
		Max: originalLimit.Max,
	}
	err = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &newLimit)
	require.NoError(t, err, "Failed to set RLIMIT_NOFILE")

	// Restore original limits when done
	defer func() {
		restoreErr := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &originalLimit)
		if restoreErr != nil {
			t.Logf("Warning: failed to restore RLIMIT_NOFILE: %v", restoreErr)
		}
	}()

	// Get free ports before we start exhausting FDs
	httpPort := getFreePort(t)
	tlsPort := getFreePort(t)
	otherPort := getFreePort(t)

	networkConfig := network.Config{
		SandboxTCPFirewallHTTPPort:  httpPort,
		SandboxTCPFirewallTLSPort:   tlsPort,
		SandboxTCPFirewallOtherPort: otherPort,
	}

	sandboxes := sandbox.NewSandboxesMap()
	meterProvider := noop.NewMeterProvider()

	proxy := New(logger.NewNopLogger(), networkConfig, sandboxes, meterProvider)

	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	defer proxyCancel()

	connCtx, connCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer connCancel()

	// Start the proxy
	proxyErr := make(chan error, 1)
	go func() {
		proxyErr <- proxy.Start(proxyCtx)
	}()

	// Wait for proxy to be ready
	require.NoError(t, waitForPort(connCtx, otherPort), "Proxy did not start in time")

	// Open many connections to exhaust FDs
	// We keep them open to maintain FD pressure
	var openConns []net.Conn
	defer func() {
		for _, c := range openConns {
			c.Close()
		}
	}()

	// Open connections until we can't anymore (FD exhaustion)
	// We expect the proxy to handle this gracefully
	const maxConnsToTry = 50
	fdExhausted := false
	for i := range maxConnsToTry {
		conn, dialErr := dial(connCtx, net.JoinHostPort("127.0.0.1", portStr(otherPort)))
		if dialErr != nil {
			// We hit FD exhaustion - this is expected
			t.Logf("FD exhaustion triggered after %d connections: %v", i, dialErr)
			fdExhausted = true

			break
		}
		openConns = append(openConns, conn)
	}

	// We should have hit FD exhaustion
	require.True(t, fdExhausted, "Expected FD exhaustion but opened all %d connections", maxConnsToTry)

	// Now free up some FDs by closing half the connections
	halfConns := len(openConns) / 2
	for i := range halfConns {
		openConns[i].Close()
	}
	openConns = openConns[halfConns:]

	// Give the system a moment to reclaim FDs
	time.Sleep(100 * time.Millisecond)

	// Now try to connect again - the proxy should have recovered
	// and accept new connections
	for i := range 3 {
		conn, dialErr := dial(connCtx, net.JoinHostPort("127.0.0.1", portStr(otherPort)))
		require.NoError(t, dialErr, "Connection %d failed after FD recovery", i)
		conn.Close()
	}

	t.Log("Proxy successfully recovered from FD exhaustion")

	// Stop the proxy
	proxyCancel()

	select {
	case err := <-proxyErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Proxy did not stop in time")
	}
}

func TestIsTransientAcceptError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"EMFILE", syscall.EMFILE, true},
		{"ENFILE", syscall.ENFILE, true},
		{"EAGAIN", syscall.EAGAIN, true},
		{"wrapped EMFILE", &net.OpError{Op: "accept", Err: syscall.EMFILE}, true},
		{"wrapped ENFILE", &net.OpError{Op: "accept", Err: syscall.ENFILE}, true},
		{"ECONNRESET", syscall.ECONNRESET, false},
		{"generic error", errors.New("some error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isTransientAcceptError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
