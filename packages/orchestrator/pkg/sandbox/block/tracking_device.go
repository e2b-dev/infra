//go:build linux

package block

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TrackingReadonlyDevice records each read offset into a PrefetchTracker
// and forwards calls to the underlying ReadonlyDevice.
type TrackingReadonlyDevice struct {
	inner   ReadonlyDevice
	tracker *PrefetchTracker
}

func NewTrackingReadonlyDevice(inner ReadonlyDevice, tracker *PrefetchTracker) *TrackingReadonlyDevice {
	return &TrackingReadonlyDevice{inner: inner, tracker: tracker}
}

func (t *TrackingReadonlyDevice) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	t.tracker.Add(off, Read)
	return t.inner.ReadAt(ctx, p, off)
}

func (t *TrackingReadonlyDevice) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	t.tracker.Add(off, Read)
	return t.inner.Slice(ctx, off, length)
}

func (t *TrackingReadonlyDevice) Size(ctx context.Context) (int64, error) { return t.inner.Size(ctx) }
func (t *TrackingReadonlyDevice) BlockSize() int64                        { return t.inner.BlockSize() }
func (t *TrackingReadonlyDevice) Header() *header.Header                  { return t.inner.Header() }
func (t *TrackingReadonlyDevice) SwapHeader(h *header.Header)             { t.inner.SwapHeader(h) }
func (t *TrackingReadonlyDevice) Close() error                            { return t.inner.Close() }
func (t *TrackingReadonlyDevice) PrefetchData() PrefetchData              { return t.tracker.PrefetchData() }
