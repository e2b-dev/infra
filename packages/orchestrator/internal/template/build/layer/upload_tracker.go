package layer

import (
	"context"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

// UploadTracker tracks in-flight uploads and coordinates the two-phase upload process:
// 1. Data upload phase: all data files are uploaded in parallel, collecting frame tables
// 2. Header finalization phase: waits for ALL data uploads, then finalizes headers
//
// This ensures frame tables from all builds are available when serializing headers
// that reference multiple builds (parent layers).
type UploadTracker struct {
	mu sync.Mutex

	// Pending frame tables shared across all layers
	pendingFrameTables *sandbox.PendingFrameTables

	// Channels for tracking data files upload completion
	dataFileUploadChs []chan struct{}

	// Channels for tracking full upload completion (for cache index saving)
	waitChs []chan struct{}
}

func NewUploadTracker() *UploadTracker {
	return &UploadTracker{
		pendingFrameTables: sandbox.NewPendingFrameTables(),
		dataFileUploadChs:  make([]chan struct{}, 0),
		waitChs:            make([]chan struct{}, 0),
	}
}

// Pending returns the shared pending frame tables.
func (t *UploadTracker) Pending() *sandbox.PendingFrameTables {
	return t.pendingFrameTables
}

// StartDataFileUpload registers that a new data files upload has started.
// Returns a function that should be called when the data files upload completes.
func (t *UploadTracker) StartDataFileUpload() (completeDataFileUpload func(), waitForAllDataFiles func(context.Context) error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Create a channel for this data upload
	ch := make(chan struct{})
	t.dataFileUploadChs = append(t.dataFileUploadChs, ch)

	var once sync.Once
	completeDataFileUpload = func() {
		once.Do(func() {
			close(ch)
		})
	}

	// waitForAllDataFiles is provided for convenience but WaitForAllDataFileUploads is preferred
	waitForAllDataFiles = func(ctx context.Context) error {
		return t.WaitForAllDataFileUploads(ctx)
	}

	return completeDataFileUpload, waitForAllDataFiles
}

// WaitForAllDataFileUploads waits for all registered data files uploads to complete.
// This should be called before finalizing headers to ensure all frame tables are available.
func (t *UploadTracker) WaitForAllDataFileUploads(ctx context.Context) error {
	t.mu.Lock()
	chs := make([]chan struct{}, len(t.dataFileUploadChs))
	copy(chs, t.dataFileUploadChs)
	t.mu.Unlock()

	for _, ch := range chs {
		select {
		case <-ch:
			// Data upload completed
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// StartUpload registers that a new full upload (data + header) has started.
// Returns functions to signal completion and wait for previous uploads.
// This is used to ensure cache index entries are saved in order.
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
