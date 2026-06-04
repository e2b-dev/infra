package filesystem

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

// collectEvents polls GetWatcherEvents until at least one event is returned or the
// deadline is reached. fsnotify delivers events asynchronously, so we can't assume
// they are available immediately after the filesystem operation.
func collectEvents(t *testing.T, ctx context.Context, svc Service, watcherID string) []*filesystem.FilesystemEvent {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := svc.GetWatcherEvents(ctx, connect.NewRequest(&filesystem.GetWatcherEventsRequest{
			WatcherId: watcherID,
		}))
		require.NoError(t, err)

		if len(resp.Msg.GetEvents()) > 0 {
			return resp.Msg.GetEvents()
		}

		time.Sleep(20 * time.Millisecond)
	}

	return nil
}

func TestWatcherIncludeEntryInfo(t *testing.T) {
	t.Parallel()

	u, err := user.Current()
	require.NoError(t, err)

	tests := []struct {
		name             string
		includeEntryInfo bool
		wantEntry        bool
	}{
		{name: "entry info included when requested", includeEntryInfo: true, wantEntry: true},
		{name: "entry info omitted when not requested", includeEntryInfo: false, wantEntry: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			svc := mockService()
			ctx := authn.SetInfo(t.Context(), u)

			created, err := svc.CreateWatcher(ctx, connect.NewRequest(&filesystem.CreateWatcherRequest{
				Path:             root,
				IncludeEntryinfo: tt.includeEntryInfo,
			}))
			require.NoError(t, err)
			watcherID := created.Msg.GetWatcherId()
			t.Cleanup(func() {
				_, _ = svc.RemoveWatcher(ctx, connect.NewRequest(&filesystem.RemoveWatcherRequest{
					WatcherId: watcherID,
				}))
			})

			// Trigger an event that leaves a stat-able entry behind.
			filePath := filepath.Join(root, "file.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0o644))

			events := collectEvents(t, ctx, svc, watcherID)
			require.NotEmpty(t, events, "expected at least one filesystem event")

			for _, e := range events {
				assert.Equal(t, "file.txt", e.GetName())

				if tt.wantEntry {
					require.NotNil(t, e.GetEntry(), "expected entry info on event %s", e.GetType())
					assert.Equal(t, "file.txt", e.GetEntry().GetName())
					assert.Equal(t, filePath, e.GetEntry().GetPath())
					assert.Equal(t, filesystem.FileType_FILE_TYPE_FILE, e.GetEntry().GetType())
				} else {
					assert.Nil(t, e.GetEntry(), "expected no entry info on event %s", e.GetType())
				}
			}
		})
	}
}

func TestWatcherIncludeEntryInfo_RemovedEntry(t *testing.T) {
	t.Parallel()

	u, err := user.Current()
	require.NoError(t, err)

	root := t.TempDir()

	// File exists before we start watching, so the only event we expect is its removal.
	filePath := filepath.Join(root, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0o644))

	svc := mockService()
	ctx := authn.SetInfo(t.Context(), u)

	created, err := svc.CreateWatcher(ctx, connect.NewRequest(&filesystem.CreateWatcherRequest{
		Path:             root,
		IncludeEntryinfo: true,
	}))
	require.NoError(t, err)
	watcherID := created.Msg.GetWatcherId()
	t.Cleanup(func() {
		_, _ = svc.RemoveWatcher(ctx, connect.NewRequest(&filesystem.RemoveWatcherRequest{
			WatcherId: watcherID,
		}))
	})

	require.NoError(t, os.Remove(filePath))

	events := collectEvents(t, ctx, svc, watcherID)
	require.NotEmpty(t, events, "expected at least one filesystem event")

	// The entry no longer exists, so even with include_entryinfo the entry must be nil.
	for _, e := range events {
		assert.Equal(t, "file.txt", e.GetName())
		assert.Nil(t, e.GetEntry(), "removed entry should not carry entry info, got event %s", e.GetType())
	}
}
