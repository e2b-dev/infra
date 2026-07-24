package process

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// newRetentionTestService builds a process Service and its Connect client
// without spawning any child processes, so — unlike newTestService — it does
// not require root. It is used to exercise the terminal-event retention cache
// in isolation.
func newRetentionTestService(t *testing.T) (spec.ProcessClient, *Service, func()) {
	t.Helper()

	cwd := t.TempDir()
	logger := zerolog.Nop()

	svc := newService(&logger, &execcontext.Defaults{
		EnvVars: utils.NewEnvVars(),
		Workdir: &cwd,
	}, cgroups.NewWorkloadFreezer(cgroups.NewNoopManager()))

	mux := http.NewServeMux()
	path, handler := spec.NewProcessHandler(svc)
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	client := spec.NewProcessClient(srv.Client(), srv.URL)

	return client, svc, srv.Close
}

func drainConnect(t *testing.T, stream *connect.ServerStreamForClient[rpc.ConnectResponse]) []*rpc.ProcessEvent {
	t.Helper()

	var events []*rpc.ProcessEvent
	for stream.Receive() {
		events = append(events, stream.Msg().GetEvent())
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())

	return events
}

// TestConnect_ServesRetainedExitByPid verifies that a Connect issued after the
// process has exited — the process is no longer in the live map, mirroring a
// gap-exit during a live-upgrade handover — still returns the Start+End pair
// with the retained exit code when selected by pid.
func TestConnect_ServesRetainedExitByPid(t *testing.T) {
	t.Parallel()

	client, svc, cleanup := newRetentionTestService(t)
	defer cleanup()

	const pid = uint32(4242)
	svc.terminated.Store(pid, &retainedExit{
		pid: pid,
		end: &rpc.ProcessEvent_EndEvent{Exited: true, ExitCode: 7, Status: "exited"},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	stream, err := client.Connect(ctx, connect.NewRequest(&rpc.ConnectRequest{
		Process: &rpc.ProcessSelector{Selector: &rpc.ProcessSelector_Pid{Pid: pid}},
	}))
	require.NoError(t, err)

	events := drainConnect(t, stream)

	require.Len(t, events, 2, "expected Start + retained End")
	assert.NotNil(t, events[0].GetStart(), "first event should be Start")
	require.NotNil(t, events[1].GetEnd(), "second event should be End")
	assert.True(t, events[1].GetEnd().GetExited())
	assert.Equal(t, int32(7), events[1].GetEnd().GetExitCode())
}

// TestConnect_ServesRetainedExitByTag verifies tag-selected lookup of a
// retained terminal event (the code-interpreter kernel is addressed by tag).
func TestConnect_ServesRetainedExitByTag(t *testing.T) {
	t.Parallel()

	client, svc, cleanup := newRetentionTestService(t)
	defer cleanup()

	tag := "kernel"
	svc.terminated.Store(99, &retainedExit{
		pid: 99,
		tag: &tag,
		end: &rpc.ProcessEvent_EndEvent{Exited: true, ExitCode: 0, Status: "exited"},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	stream, err := client.Connect(ctx, connect.NewRequest(&rpc.ConnectRequest{
		Process: &rpc.ProcessSelector{Selector: &rpc.ProcessSelector_Tag{Tag: tag}},
	}))
	require.NoError(t, err)

	events := drainConnect(t, stream)

	require.Len(t, events, 2)
	assert.Equal(t, uint32(99), events[0].GetStart().GetPid())
	require.NotNil(t, events[1].GetEnd())
	assert.True(t, events[1].GetEnd().GetExited())
}

// TestConnect_UnknownProcessNotFound verifies the regression guard: a Connect
// for a process that is neither live nor retained still returns NotFound.
func TestConnect_UnknownProcessNotFound(t *testing.T) {
	t.Parallel()

	client, _, cleanup := newRetentionTestService(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	stream, err := client.Connect(ctx, connect.NewRequest(&rpc.ConnectRequest{
		Process: &rpc.ProcessSelector{Selector: &rpc.ProcessSelector_Pid{Pid: 1}},
	}))
	require.NoError(t, err)

	for stream.Receive() {
	}
	require.Error(t, stream.Err())
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(stream.Err()))
	_ = stream.Close()
}
