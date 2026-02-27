package layer

import (
	"context"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

// UploadTracker tracks in-flight layer uploads and provides ordering guarantees.
//
// Each layer's upload proceeds as: data files → wait for previous → compressed headers → save cache.
// waitForPreviousUploads ensures that by the time layer N finalizes its compressed headers,
// all upstream layers (0..N-1) have completed both their data uploads and header uploads,
// so all upstream frame tables are available in the shared PendingFrameTables.
type UploadTracker struct {
	mu      sync.Mutex
	waitChs []chan struct{}

	// pending collects frame tables from compressed uploads across all layers.
	pending *sandbox.PendingFrameTables
}

func NewUploadTracker() *UploadTracker {
	return &UploadTracker{
		waitChs: make([]chan struct{}, 0),
		pending: &sandbox.PendingFrameTables{},
	}
}

// Pending returns the shared PendingFrameTables for collecting frame tables.
func (t *UploadTracker) Pending() *sandbox.PendingFrameTables {
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
