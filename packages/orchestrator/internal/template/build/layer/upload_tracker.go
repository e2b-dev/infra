package layer

import (
	"context"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

// UploadTracker tracks in-flight uploads and allows waiting for all previous uploads to complete.
// This prevents race conditions where a layer's cache entry is saved before its
// dependencies (previous layers) are fully uploaded.
//
// It also owns a shared PendingBuildInfo that collects frame tables from compressed
// uploads across all layers. waitForPreviousUploads guarantees that by the time
// layer N finalizes its compressed headers, all upstream layers (0..N-1) have
// completed both their data and header uploads, so all upstream frame tables
// are available for cross-pollination.
type UploadTracker struct {
	mu      sync.Mutex
	waitChs []chan struct{}

	// pending collects frame tables from compressed uploads across all layers.
	pending *sandbox.PendingBuildInfo
}

func NewUploadTracker() *UploadTracker {
	return &UploadTracker{
		waitChs: make([]chan struct{}, 0),
		pending: &sandbox.PendingBuildInfo{},
	}
}

// Pending returns the shared PendingBuildInfo for collecting frame tables.
func (t *UploadTracker) Pending() *sandbox.PendingBuildInfo {
	return t.pending
}

// StartUpload registers that a new upload has started.
// Returns a function that should be called when the upload completes.
func (t *UploadTracker) StartUpload() (complete func(), waitForPrevious func(context.Context) error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Create a channel for this upload
	ch := make(chan struct{})
	t.waitChs = append(t.waitChs, ch)

	// Capture the channels we need to wait for (all previous uploads)
	previousChs := make([]chan struct{}, len(t.waitChs)-1)
	copy(previousChs, t.waitChs[:len(t.waitChs)-1])

	complete = func() {
		close(ch)
	}

	waitForPrevious = func(ctx context.Context) error {
		for _, prevCh := range previousChs {
			select {
			case <-prevCh:
				// Previous upload completed
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		return nil
	}

	return complete, waitForPrevious
}
