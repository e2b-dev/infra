package filesystem

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

// TestWatcherHandoverReArm verifies the live-upgrade watcher re-arm: a watcher
// exported from one service and imported into a fresh one keeps its id, its
// config, and continues to deliver new filesystem events.
func TestWatcherHandoverReArm(t *testing.T) {
	t.Parallel()

	u, err := user.Current()
	require.NoError(t, err)

	root := t.TempDir()
	ctx := authn.SetInfo(t.Context(), u)

	src := mockService()
	created, err := src.CreateWatcher(ctx, connect.NewRequest(&filesystem.CreateWatcherRequest{
		Path:      root,
		Recursive: false,
	}))
	require.NoError(t, err)
	wid := created.Msg.GetWatcherId()

	// Export from the outgoing service, import into the incoming one.
	blob := src.ExportWatchers()
	require.NotEmpty(t, blob)

	dst := mockService()
	dst.ImportWatchers(blob)

	// The re-armed watcher keeps the same id...
	_, ok := dst.watchers.Load(wid)
	assert.True(t, ok, "re-armed watcher should preserve its id")

	// ...and delivers events for changes made after the handover.
	require.NoError(t, os.WriteFile(filepath.Join(root, "after.txt"), []byte("x"), 0o644))
	events := collectEvents(t, ctx, dst, wid)
	require.NotEmpty(t, events, "re-armed watcher should deliver post-handover events under the preserved id")
}

// TestWatcherHandoverPreservesPending verifies buffered events not yet fetched
// survive the export/import round-trip.
func TestWatcherHandoverPreservesPending(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := mockService()

	fw, err := CreateFileWatcher(t.Context(), src.logger, root, false, false)
	require.NoError(t, err)
	fw.Lock.Lock()
	fw.Events = append(fw.Events, &filesystem.FilesystemEvent{
		Name: "pending.txt",
		Type: filesystem.EventType_EVENT_TYPE_CREATE,
	})
	fw.Lock.Unlock()
	src.watchers.Store("wpending", fw)

	dst := mockService()
	dst.ImportWatchers(src.ExportWatchers())

	got, ok := dst.watchers.Load("wpending")
	require.True(t, ok)
	got.Lock.Lock()
	defer got.Lock.Unlock()
	require.GreaterOrEqual(t, len(got.Events), 1)
	assert.Equal(t, "pending.txt", got.Events[0].GetName())
}
