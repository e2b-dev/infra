package process

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process/processconnect"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func newTestService(t *testing.T) (spec.ProcessClient, func()) {
	t.Helper()

	// handler.New sets SysProcAttr.Credential to switch uid/gid,
	// which requires root.
	if os.Geteuid() != 0 {
		t.Skip("skipping: requires root (handler.New sets SysProcAttr.Credential)")
	}

	u, err := user.Current()
	require.NoError(t, err)

	cwd := t.TempDir()
	logger := zerolog.Nop()

	svc := newService(&logger, &execcontext.Defaults{
		EnvVars: utils.NewEnvVars(),
		User:    u.Username,
		Workdir: &cwd,
	}, cgroups.NewNoopManager())

	mux := http.NewServeMux()
	path, handler := spec.NewProcessHandler(svc)
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	client := spec.NewProcessClient(srv.Client(), srv.URL)

	return client, srv.Close
}

// TestStart_ShortCommand verifies that a short-lived command streams
// start, data, and end events, then the handler returns cleanly.
func TestStart_ShortCommand(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "echo",
			Args: []string{"hello"},
		},
	}))
	require.NoError(t, err)

	var events []*rpc.ProcessEvent
	for stream.Receive() {
		events = append(events, stream.Msg().GetEvent())
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())

	// Expect at least a start event and an end event.
	require.GreaterOrEqual(t, len(events), 2, "expected at least start + end events")
	assert.NotNil(t, events[0].GetStart(), "first event should be Start")
	assert.NotNil(t, events[len(events)-1].GetEnd(), "last event should be End")
}

// TestSendSignal_KillsProcessTree verifies that with child_processes set,
// killing a command also terminates the child processes it spawned. envd's
// per-process handle only signaled the leader, so children kept running after a
// kill; non-PTY commands now run in their own process group and an opted-in
// SIGKILL reaches the whole tree.
func TestSendSignal_KillsProcessTree(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	// The leader spawns two long-lived children and waits on them.
	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "/bin/sh",
			Args: []string{"-c", "sleep 120 & sleep 120 & wait"},
		},
	}))
	require.NoError(t, err)

	// Read the start event to capture the leader pid.
	require.True(t, stream.Receive(), "expected a start event")
	pid := stream.Msg().GetEvent().GetStart().GetPid()
	require.NotZero(t, pid)

	// Wait for the children to come up, then capture their pids.
	var childPids []int
	require.Eventually(t, func() bool {
		childPids = childrenOf(t, int(pid))

		return len(childPids) == 2
	}, 5*time.Second, 50*time.Millisecond, "expected leader to spawn two children")

	// Kill the command with child_processes set — takes down the whole group.
	_, err = client.SendSignal(ctx, connect.NewRequest(&rpc.SendSignalRequest{
		Process: &rpc.ProcessSelector{
			Selector: &rpc.ProcessSelector_Pid{Pid: pid},
		},
		Signal:         rpc.Signal_SIGNAL_SIGKILL,
		ChildProcesses: true,
	}))
	require.NoError(t, err)

	// Drain the stream so the leader is reaped.
	for stream.Receive() {
	}
	_ = stream.Close()

	// The leader and every child must be gone.
	require.Eventually(t, func() bool {
		return !slices.ContainsFunc(childPids, processAlive)
	}, 5*time.Second, 50*time.Millisecond, "child processes should be killed with the leader")
}

// TestSendSignal_LeaderOnlyByDefault verifies that without child_processes the
// signal targets only the leader, leaving the children it spawned running (the
// original, opt-out behavior).
func TestSendSignal_LeaderOnlyByDefault(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	// The leader spawns two children that outlive it (no wait), so killing only
	// the leader leaves them reparented but alive.
	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "/bin/sh",
			Args: []string{"-c", "sleep 120 & sleep 120 & wait"},
		},
	}))
	require.NoError(t, err)

	require.True(t, stream.Receive(), "expected a start event")
	pid := stream.Msg().GetEvent().GetStart().GetPid()
	require.NotZero(t, pid)

	var childPids []int
	require.Eventually(t, func() bool {
		childPids = childrenOf(t, int(pid))

		return len(childPids) == 2
	}, 5*time.Second, 50*time.Millisecond, "expected leader to spawn two children")

	// Kill without child_processes — only the leader should be signaled.
	_, err = client.SendSignal(ctx, connect.NewRequest(&rpc.SendSignalRequest{
		Process: &rpc.ProcessSelector{
			Selector: &rpc.ProcessSelector_Pid{Pid: pid},
		},
		Signal: rpc.Signal_SIGNAL_SIGKILL,
	}))
	require.NoError(t, err)

	for stream.Receive() {
	}
	_ = stream.Close()

	// The leader is gone but its children keep running; clean them up after.
	require.Eventually(t, func() bool {
		return !processAlive(int(pid))
	}, 5*time.Second, 50*time.Millisecond, "leader should be killed")

	for _, cp := range childPids {
		assert.True(t, processAlive(cp), "child %d should still be alive after a leader-only kill", cp)
	}

	// Clean up the orphaned children the test intentionally left running.
	for _, cp := range childPids {
		_ = syscall.Kill(cp, syscall.SIGKILL)
	}
}

// TestStart_TimeoutKillsProcessTreeWhenOptedIn verifies that when a command is
// started with child_processes set, a request timeout tears down its whole
// process tree, not just the leader.
func TestStart_TimeoutKillsProcessTreeWhenOptedIn(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	// The context deadline is propagated as Connect-Timeout-Ms, which envd uses
	// as the process timeout. Keep it short so the test doesn't drag.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "/bin/sh",
			Args: []string{"-c", "sleep 120 & sleep 120 & wait"},
		},
		ChildProcesses: true,
	}))
	require.NoError(t, err)

	require.True(t, stream.Receive(), "expected a start event")
	pid := stream.Msg().GetEvent().GetStart().GetPid()
	require.NotZero(t, pid)

	// Capture the children before the timeout fires.
	var childPids []int
	require.Eventually(t, func() bool {
		childPids = childrenOf(t, int(pid))

		return len(childPids) == 2
	}, 2*time.Second, 50*time.Millisecond, "expected leader to spawn two children")

	// Drain until the request times out and the stream ends.
	for stream.Receive() {
	}
	_ = stream.Close()

	// The timeout must take down the whole tree because child_processes was set.
	require.Eventually(t, func() bool {
		return !processAlive(int(pid)) && !slices.ContainsFunc(childPids, processAlive)
	}, 5*time.Second, 50*time.Millisecond, "timeout should kill the whole process tree when opted in")
}

// childrenOf returns the pids whose parent is ppid, read from /proc.
func childrenOf(t *testing.T, ppid int) []int {
	t.Helper()

	entries, err := os.ReadDir("/proc")
	require.NoError(t, err)

	var children []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			// The process may have exited between listing and reading.
			continue
		}

		// /proc/<pid>/stat fields: pid (comm) state ppid ...
		// comm can contain spaces/parens, so parse after the closing paren.
		stat := string(data)
		idx := strings.LastIndex(stat, ")")
		if idx == -1 {
			continue
		}

		fields := strings.Fields(stat[idx+1:])
		// fields[0]=state, fields[1]=ppid
		if len(fields) < 2 {
			continue
		}

		if parsed, err := strconv.Atoi(fields[1]); err == nil && parsed == ppid {
			children = append(children, pid)
		}
	}

	return children
}

// processAlive reports whether the given pid exists and is not a zombie.
// kill(pid, 0) succeeds for killed-but-unreaped processes, so we read the
// process state from /proc and treat zombies (state "Z") as not alive — the
// orphaned children may not be reaped promptly depending on who PID 1 is.
func processAlive(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		// Gone from /proc entirely — definitely not alive.
		return false
	}

	// /proc/<pid>/stat: pid (comm) state ...; comm can contain spaces/parens,
	// so the state is the first field after the closing paren.
	stat := string(data)
	idx := strings.LastIndex(stat, ")")
	if idx == -1 {
		return false
	}

	fields := strings.Fields(stat[idx+1:])

	return len(fields) > 0 && fields[0] != "Z"
}

// TestStart_ClientDisconnectMidStream verifies that when a client
// cancels mid-stream, the handler returns without racing.  This is
// the scenario that caused the nil-pointer panic in production:
// the outer handler must wait for the inner sender goroutine to
// finish before returning.
//
// Run with -race to verify no data race.
func TestStart_ClientDisconnectMidStream(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Use a long-running command so there's data flowing when we cancel.
	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd: "yes",
		},
	}))
	require.NoError(t, err)

	// Receive a few events to ensure the stream is established and
	// the inner sender goroutine is actively calling stream.Send.
	for range 3 {
		if !stream.Receive() {
			break
		}
	}

	// Cancel the client context, simulating an abrupt disconnect.
	// Before the fix, this would cause the outer handler to return
	// while the inner goroutine was still mid-Send, leading to a
	// nil-pointer panic in bufio.(*Writer).Flush.
	cancel()

	// Drain remaining events — the stream should close cleanly.
	for stream.Receive() {
	}
	_ = stream.Close()
}
