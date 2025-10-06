package envd

import (
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestWatcher(t *testing.T) {
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	envdClient := setup.GetEnvdClient(t, t.Context())

	// Create a test directory
	watchDir := "/tmp/watch_test"
	utils.CreateDir(t, sbx, watchDir)

	// Create watcher
	createReq := connect.NewRequest(&filesystem.CreateWatcherRequest{
		Path:      watchDir,
		Recursive: false,
	})
	setup.SetSandboxHeader(createReq.Header(), sbx.SandboxID)
	setup.SetUserHeader(createReq.Header(), "user")

	createResp, err := envdClient.FilesystemClient.CreateWatcher(t.Context(), createReq)
	require.NoError(t, err)
	require.NotNil(t, createResp)
	assert.NotEmpty(t, createResp.Msg.GetWatcherId())
	watcherId := createResp.Msg.GetWatcherId()

	// Get events (should be empty initially)
	getReq := connect.NewRequest(&filesystem.GetWatcherEventsRequest{
		WatcherId: watcherId,
	})
	setup.SetSandboxHeader(getReq.Header(), sbx.SandboxID)
	setup.SetUserHeader(getReq.Header(), "user")

	getResp, err := envdClient.FilesystemClient.GetWatcherEvents(t.Context(), getReq)
	require.NoError(t, err)
	assert.Empty(t, getResp.Msg.GetEvents())

	testFile := fmt.Sprintf("%s/test.txt", watchDir)
	utils.UploadFile(t, t.Context(), sbx, envdClient, testFile, "hello world")

	var events []*filesystem.FilesystemEvent
	require.Eventually(t, func() bool {
		getResp, err := envdClient.FilesystemClient.GetWatcherEvents(t.Context(), getReq)
		require.NoError(t, err)
		events = getResp.Msg.GetEvents()
		return len(events) > 0
	}, 5*time.Second, 20*time.Millisecond, "Expected to receive file system events")
	require.NotEmpty(t, events)
	assert.Equal(t, filesystem.EventType_EVENT_TYPE_CREATE, events[0].GetType())
	assert.Equal(t, "test.txt", events[0].GetName())

	// Remove watcher
	removeReq := connect.NewRequest(&filesystem.RemoveWatcherRequest{
		WatcherId: watcherId,
	})
	setup.SetSandboxHeader(removeReq.Header(), sbx.SandboxID)
	setup.SetUserHeader(removeReq.Header(), "user")

	removeResp, err := envdClient.FilesystemClient.RemoveWatcher(t.Context(), removeReq)
	require.NoError(t, err)
	assert.NotNil(t, removeResp)

	// Verify watcher is removed (should fail)
	getResp2, err := envdClient.FilesystemClient.GetWatcherEvents(t.Context(), getReq)
	require.Error(t, err)
	assert.Nil(t, getResp2)
}
