//go:build linux

package fc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Cancellation while waiting for readiness preserves the handler's exit cause.
func TestLoadSnapshotKeepsUffdExitCause(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "fc") //nolint:usetesting // t.TempDir embeds the test name and overflows the unix sun_path limit
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := dir + "/fc.sock"
	listener, err := new(net.ListenConfig).Listen(t.Context(), "unix", socket)
	require.NoError(t, err)

	loaded := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /snapshot/load", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		close(loaded)
	})
	server := &http.Server{Handler: mux} //nolint:gosec // no timeouts needed for a stub on a private socket
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	go func() {
		<-loaded
		// Give the client time to read the answer, so the exit lands in the wait for uffd ready.
		time.Sleep(100 * time.Millisecond)
		cancel(fmt.Errorf("uffd process exited: failed to wrap memfd: mmap memfd: %w", unix.ENOMEM))
	}()

	err = newApiClient(socket).loadSnapshot(ctx, dir+"/uffd.sock", make(chan struct{}), utils.NewErrorOnce(), stubSnapfile(dir+"/snapfile"), false, false, "", "team-1")
	require.ErrorIs(t, err, unix.ENOMEM)
}

// The exit watcher may not have canceled the start context when Ready closes.
func TestLoadSnapshotChecksUffdExitAfterReady(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		exited  bool
		exitErr error
	}{
		{name: "ready"},
		{name: "failed", exited: true, exitErr: block.GuestMemoryError{Err: unix.ENOMEM}},
		{name: "stopped", exited: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "fc") //nolint:usetesting // unix socket path limit
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			socket := dir + "/fc.sock"
			listener, err := new(net.ListenConfig).Listen(t.Context(), "unix", socket)
			require.NoError(t, err)

			exit := utils.NewErrorOnce()
			ready := make(chan struct{})
			mux := http.NewServeMux()
			mux.HandleFunc("PUT /snapshot/load", func(w http.ResponseWriter, _ *http.Request) {
				if tt.exited {
					_ = exit.SetError(tt.exitErr)
				}
				close(ready)
				w.WriteHeader(http.StatusNoContent)
			})
			server := &http.Server{Handler: mux} //nolint:gosec // private test socket
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close() })

			err = newApiClient(socket).loadSnapshot(t.Context(), dir+"/uffd.sock", ready, exit, stubSnapfile(dir+"/snapfile"), false, false, "", "team-1")
			if !tt.exited {
				require.NoError(t, err)

				return
			}
			require.ErrorContains(t, err, "uffd exited while loading snapshot")
			if tt.exitErr != nil {
				require.ErrorIs(t, err, unix.ENOMEM)
				var memoryErr block.GuestMemoryError
				require.ErrorAs(t, err, &memoryErr)
			}
		})
	}
}
