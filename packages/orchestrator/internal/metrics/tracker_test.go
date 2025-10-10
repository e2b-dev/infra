package metrics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

func TestTracker_CreateMissingDirectory(t *testing.T) {
	tempDir := t.TempDir()
	metricsDir := filepath.Join(tempDir, "metrics")

	selfWriteInterval := time.Millisecond * 100

	tracker, err := NewTracker(selfWriteInterval)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), selfWriteInterval*2)
	t.Cleanup(cancel)

	// run the tracker for a bit
	err = tracker.Run(ctx, metricsDir)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTrackerRoundTrip(t *testing.T) {
	tempDir := t.TempDir()

	os.WriteFile(filepath.Join(tempDir, "990.json"), []byte(`{"diskBytes": 0, "memoryBytes": 0, "sandboxes": 0, "vcpus": 1}`), 0o644)

	tracker, err := NewTracker(time.Millisecond * 100)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// start the tracker in the background
	go func() {
		err := tracker.Run(ctx, tempDir)
		assert.ErrorIs(t, err, context.Canceled)
	}()

	// write one file
	otherJSON1 := toJSON(t, Allocations{
		DiskBytes:   1 * megabytes,
		MemoryBytes: 2 * megabytes,
		Sandboxes:   3,
		VCPUs:       4,
	})
	err = os.WriteFile(filepath.Join(tempDir, "999.json"), otherJSON1, 0o644)
	require.NoError(t, err)

	// wait for the watcher to pick up the changes
	time.Sleep(time.Millisecond * 100)

	allocated := tracker.TotalAllocated()
	assert.Equal(t, Allocations{
		DiskBytes:   1 * megabytes,
		MemoryBytes: 2 * megabytes,
		Sandboxes:   3,
		VCPUs:       5,
	}, allocated)

	// write a second file
	otherJSON2 := toJSON(t, Allocations{
		DiskBytes:   1 * megabytes,
		MemoryBytes: 2 * megabytes,
		Sandboxes:   3,
		VCPUs:       4,
	})
	err = os.WriteFile(filepath.Join(tempDir, "998.json"), otherJSON2, 0o644)
	require.NoError(t, err)

	// wait for the watcher to pick up the changes
	time.Sleep(time.Millisecond * 100)

	// verify the total is the combination of both json files
	allocated = tracker.TotalAllocated()
	assert.Equal(t, Allocations{
		DiskBytes:   2 * megabytes,
		MemoryBytes: 4 * megabytes,
		Sandboxes:   6,
		VCPUs:       9,
	}, allocated)

	// modify the second file
	otherJSON2 = toJSON(t, Allocations{
		DiskBytes:   3 * megabytes,
		MemoryBytes: 4 * megabytes,
		Sandboxes:   5,
		VCPUs:       6,
	})
	err = os.WriteFile(filepath.Join(tempDir, "998.json"), otherJSON2, 0o644)
	require.NoError(t, err)

	// wait for the watcher to pick up the changes
	time.Sleep(time.Millisecond * 100)

	// verify the total is the combination of both json files
	allocated = tracker.TotalAllocated()
	assert.Equal(t, Allocations{
		DiskBytes:   4 * megabytes,
		MemoryBytes: 6 * megabytes,
		Sandboxes:   8,
		VCPUs:       11,
	}, allocated)

	// add a local sandbox
	tracker.OnInsert(&sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.Config{
				Vcpu:            1,
				RamMB:           2,
				TotalDiskSizeMB: 3,
			},
		},
	})

	// wait for the watcher to pick up the changes
	time.Sleep(time.Millisecond * 100)

	// verify the total is the combination of both json files
	allocated = tracker.TotalAllocated()
	assert.Equal(t, Allocations{
		DiskBytes:   7 * megabytes,
		MemoryBytes: 8 * megabytes,
		Sandboxes:   9,
		VCPUs:       12,
	}, allocated)

	err = os.Remove(filepath.Join(tempDir, "998.json"))
	require.NoError(t, err)

	// wait for the watcher to pick up the changes
	time.Sleep(time.Millisecond * 100)

	// ensure metrics are removed
	allocated = tracker.TotalAllocated()
	assert.Equal(t, Allocations{
		DiskBytes:   4 * megabytes,
		MemoryBytes: 4 * megabytes,
		Sandboxes:   4,
		VCPUs:       6,
	}, allocated)

	selfPath := tracker.makeSelfPath(tempDir)

	// ensure the self file has been created
	_, err = os.Stat(selfPath)
	require.NoError(t, err)

	cancel()

	time.Sleep(time.Millisecond * 100)

	// ensure the self file has been removed
	_, err = os.Stat(selfPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestTracker_handleWriteSelf(t *testing.T) {
	tempDir := t.TempDir()

	tracker, err := NewTracker(10 * time.Second)
	require.NoError(t, err)

	tracker.OnInsert(&sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.Config{
				Vcpu:            1,
				RamMB:           2,
				TotalDiskSizeMB: 3,
			},
		},
	})

	selfPath := tracker.makeSelfPath(tempDir)

	err = tracker.handleWriteSelf(selfPath)
	require.NoError(t, err)

	data, err := os.ReadFile(selfPath)
	require.NoError(t, err)

	var allocations Allocations
	err = json.Unmarshal(data, &allocations)
	require.NoError(t, err)
	assert.Equal(t, Allocations{
		DiskBytes:   3 * megabytes,
		MemoryBytes: 2 * megabytes,
		Sandboxes:   1,
		VCPUs:       1,
	}, allocations)
}

const megabytes = 1024 * 1024

func toJSON[T any](t *testing.T, model T) []byte {
	t.Helper()

	data, err := json.Marshal(model)
	require.NoError(t, err)
	return data
}
