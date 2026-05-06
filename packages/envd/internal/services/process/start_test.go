package process

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
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
