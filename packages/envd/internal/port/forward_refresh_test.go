package port

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"
	gopsnet "github.com/shirou/gopsutil/v4/net"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
)

const fakeSocatEnv = "ENVD_PORT_FAKE_SOCAT"

// TestFakeSocatHelperProcess is not a test. The forwarder tests put a `socat`
// shim on PATH that re-executes this test binary, so the spawned "socat" is a
// process that really binds the listen address and really holds it until it is
// killed. That is what makes the forwarding observable end to end.
//
//nolint:paralleltest // helper process, must not run alongside anything
func TestFakeSocatHelperProcess(t *testing.T) {
	if os.Getenv(fakeSocatEnv) != "1" {
		t.Skip("helper process, only runs through the socat shim")
	}

	addr, ok := parseFakeSocatListen(os.Args)
	if !ok {
		os.Exit(2)
	}

	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		// Address already in use, exactly what a second socat bound to the
		// same address would do.
		os.Exit(1)
	}
	defer l.Close()

	// Hold the address until the forwarder kills us.
	time.Sleep(time.Hour)
}

// parseFakeSocatListen extracts host:port out of a socat listen spec such as
// "TCP4-LISTEN:4000,bind=127.0.0.1,reuseaddr,fork".
func parseFakeSocatListen(args []string) (string, bool) {
	for _, a := range args {
		spec, found := strings.CutPrefix(a, "TCP4-LISTEN:")
		if !found {
			continue
		}

		parts := strings.Split(spec, ",")

		var host string
		for _, opt := range parts[1:] {
			if bind, isBind := strings.CutPrefix(opt, "bind="); isBind {
				host = bind
			}
		}

		return net.JoinHostPort(host, parts[0]), true
	}

	return "", false
}

// newRefreshTestForwarder builds a forwarder whose socat is the shim above and
// whose forwarded address is loopback, so the shim can bind it for real.
func newRefreshTestForwarder(t *testing.T) *Forwarder {
	t.Helper()

	self, err := os.Executable()
	require.NoError(t, err)

	dir := t.TempDir()
	shim := fmt.Sprintf("#!/bin/sh\nexec %q -test.run='^TestFakeSocatHelperProcess$' -- \"$@\"\n", self)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "socat"), []byte(shim), 0o755))

	t.Setenv(fakeSocatEnv, "1")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	l := zerolog.Nop()
	f := &Forwarder{
		logger:        &l,
		ports:         make(map[string]*PortToForward),
		cgroupManager: cgroups.NewNoopManager(),
		sourceIP:      net.IPv4(127, 0, 0, 1),
	}

	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		for _, p := range f.ports {
			if pid := p.socatPID(); pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	})

	return f
}

// freePort returns a port that is unused at the time of the call.
func freePort(t *testing.T) uint32 {
	t.Helper()

	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	return uint32(port)
}

func listening(pid int32, port uint32) gopsnet.ConnectionStat {
	return gopsnet.ConnectionStat{
		Family: syscall.AF_INET,
		Status: "LISTEN",
		Laddr:  gopsnet.Addr{IP: "127.0.0.1", Port: port},
		Pid:    pid,
	}
}

// requireForwarded waits until the forwarded address is (or is no longer)
// accepting connections. Spawning the shim is asynchronous, so this polls.
func requireForwarded(t *testing.T, port uint32, want bool) {
	t.Helper()

	addr := net.JoinHostPort("127.0.0.1", strconv.FormatUint(uint64(port), 10))

	dialer := net.Dialer{Timeout: 200 * time.Millisecond}

	var last bool
	require.Eventually(t, func() bool {
		c, err := dialer.DialContext(t.Context(), "tcp", addr)
		if c != nil {
			c.Close()
		}
		last = err == nil

		return last == want
	}, 10*time.Second, 25*time.Millisecond, "port %d forwarded=%v, want %v", port, last, want)
}

// TestForwarderReForwardsPortAfterScanMiss covers a listening socket that drops
// out of a single scan and comes back under the same pid: an app closing and
// reopening its socket, or a scan that cannot map the socket's inode to a pid.
// The forward has to come back, otherwise the app stays unreachable through the
// proxy for the rest of the sandbox's life even though it is listening.
//
//nolint:paralleltest // newRefreshTestForwarder uses t.Setenv
func TestForwarderReForwardsPortAfterScanMiss(t *testing.T) {
	f := newRefreshTestForwarder(t)
	port := freePort(t)

	f.refresh(t.Context(), []gopsnet.ConnectionStat{listening(100, port)})
	requireForwarded(t, port, true)

	// The listener drops out of one scan and the forward is torn down.
	f.refresh(t.Context(), nil)
	requireForwarded(t, port, false)
	assert.Empty(t, f.ports, "a torn down forward must not stay in the ports map")

	// The next scan reports it again.
	f.refresh(t.Context(), []gopsnet.ConnectionStat{listening(100, port)})
	requireForwarded(t, port, true)
}

// TestForwarderReplacesForwardWhenListenerPidChanges covers an app restarting
// inside the sandbox: same port, new pid. The port has to keep being forwarded
// and the previous listener must not be left behind in the ports map.
//
//nolint:paralleltest // newRefreshTestForwarder uses t.Setenv
func TestForwarderReplacesForwardWhenListenerPidChanges(t *testing.T) {
	f := newRefreshTestForwarder(t)
	port := freePort(t)

	f.refresh(t.Context(), []gopsnet.ConnectionStat{listening(100, port)})
	requireForwarded(t, port, true)

	f.refresh(t.Context(), []gopsnet.ConnectionStat{listening(200, port)})
	requireForwarded(t, port, true)

	// Steady state after the restart.
	f.refresh(t.Context(), []gopsnet.ConnectionStat{listening(200, port)})
	requireForwarded(t, port, true)

	assert.Len(t, f.ports, 1, "a single listener must not leave stale entries behind")
}
