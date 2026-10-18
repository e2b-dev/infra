//go:build linux

package fc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A uffd handler that exits after Firecracker loaded the snapshot ends the
// wait for it with its exit error as the cause. The cause must reach the
// caller: Create tells a node out of memory (ENOMEM from the memfd mmap) from
// a failed create by it.
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
		cancel(fmt.Errorf("uffd process exited: failed to wrap memfd: mmap memfd: %w", syscall.ENOMEM))
	}()

	// uffdReady is never closed: the handler exits before it is ready.
	err = newApiClient(socket).loadSnapshot(ctx, dir+"/uffd.sock", make(chan struct{}), stubSnapfile(dir+"/snapfile"), false, false, "", "team-1")
	require.ErrorIs(t, err, syscall.ENOMEM)
}
