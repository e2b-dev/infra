package process

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"runtime"
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
		EnvVars: utils.NewMap[string, string](),
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use a long-running command so there's data flowing when we cancel.
	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "sh",
			Args: []string{"-c", "yes"},
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

// TestStart_ProcessSurvivesClientDisconnect verifies that a child
// process keeps running after the Start RPC client disconnects.
// This is intentional: procCtx is context.Background() so clients
// can reconnect via Connect later.
func TestStart_ProcessSurvivesClientDisconnect(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "timeout",
			Args: []string{"5", "yes"},
		},
	}))
	require.NoError(t, err)

	// Wait for the start event to get the PID.
	require.True(t, stream.Receive(), "expected start event")
	startEvt := stream.Msg().GetEvent().GetStart()
	require.NotNil(t, startEvt)
	pid := int(startEvt.GetPid())
	require.Positive(t, pid)

	// Disconnect the client.
	cancel()
	for stream.Receive() {
	}
	_ = stream.Close()

	time.Sleep(200 * time.Millisecond)

	// The child must still be alive — this is the Start-then-Connect contract.
	assert.True(t, processAlive(pid),
		"child process %d should survive client disconnect", pid)

	// Clean up.
	proc, _ := os.FindProcess(pid)
	_ = proc.Kill()
}

// TestStart_DisconnectStormHeapGrowth demonstrates the memory leak
// from bug #2: each abandoned Start RPC with a fast-producing child
// leaves behind channel buffers and reader goroutines that accumulate
// memory.  We run several disconnect cycles and assert that heap
// usage stays bounded.
func TestStart_DisconnectStormHeapGrowth(t *testing.T) {
	t.Parallel()

	client, cleanup := newTestService(t)
	defer cleanup()

	const cycles = 5

	// Force a GC and record baseline heap.
	runtime.GC() //nolint:revive // intentional: need accurate heap baseline
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	var pids []int

	for i := range cycles {
		ctx, cancel := context.WithCancel(context.Background())

		stream, err := client.Start(ctx, connect.NewRequest(&rpc.StartRequest{
			Process: &rpc.ProcessConfig{
				Cmd:  "timeout",
				Args: []string{"10", "yes"},
			},
		}))
		require.NoError(t, err, "cycle %d", i)

		// Wait for the start event to get the PID.
		require.True(t, stream.Receive(), "cycle %d: expected start event", i)
		startEvt := stream.Msg().GetEvent().GetStart()
		require.NotNil(t, startEvt, "cycle %d", i)
		pids = append(pids, int(startEvt.GetPid()))

		// Receive a few data events so the producer is actively
		// writing into the handler's channel buffers.
		for range 5 {
			if !stream.Receive() {
				break
			}
		}

		// Disconnect.
		cancel()
		for stream.Receive() {
		}
		_ = stream.Close()

		// Let the orphaned producer fill buffers for a moment.
		time.Sleep(500 * time.Millisecond)
	}

	// Measure heap after all cycles.
	runtime.GC() //nolint:revive // intentional: need accurate heap measurement
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	heapGrowthMiB := float64(after.HeapInuse-baseline.HeapInuse) / (1024 * 1024)
	t.Logf("heap: baseline=%.1f MiB after=%.1f MiB growth=%.1f MiB (%d cycles)",
		float64(baseline.HeapInuse)/(1024*1024),
		float64(after.HeapInuse)/(1024*1024),
		heapGrowthMiB, cycles)

	// BUG: with procCtx=context.Background(), each orphaned `yes`
	// process keeps pumping ~32 KiB chunks into channel buffers
	// that nobody drains.  Heap grows roughly linearly with time
	// and number of cycles.  A healthy implementation should stay
	// well under 50 MiB for 5 cycles.
	const maxHeapGrowthMiB = 50.0
	if heapGrowthMiB > maxHeapGrowthMiB {
		t.Errorf("heap grew %.1f MiB over %d disconnect cycles "+
			"(limit %.1f MiB); orphaned handlers leaking memory",
			heapGrowthMiB, cycles, maxHeapGrowthMiB)
	}

	// Kill orphaned processes.
	for _, pid := range pids {
		if processAlive(pid) {
			proc, _ := os.FindProcess(pid)
			_ = proc.Kill()
		}
	}
}

// processAlive checks whether a process with the given PID exists.
func processAlive(pid int) bool {
	// /proc/<pid>/stat exists iff the process is alive (Linux-specific).
	_, err := os.Stat(fmt.Sprintf("/proc/%d/stat", pid))

	return err == nil
}
