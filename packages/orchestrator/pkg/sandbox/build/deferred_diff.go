//go:build linux

package build

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// deferredDiff is a Diff whose backing data is produced asynchronously. It is
// returned synchronously from a pause that seals the rootfs in the background:
// the cache key and block size are known up front (so DiffStore.Add and the
// upload's compress-config validation work immediately), while every data-
// bearing method blocks on the inner promise until the background seal resolves
// the real Diff.
//
// The producer MUST always resolve the promise — with the sealed Diff on success
// or an error on failure — otherwise the data methods (and Close) block forever.
type deferredDiff struct {
	cacheKey  DiffStoreKey
	blockSize int64
	inner     *utils.SetOnce[Diff]
}

var _ Diff = (*deferredDiff)(nil)

// NewDeferredDiff wraps a promise of a Diff. cacheKey and blockSize are the
// synchronously-known identity of the diff; inner is resolved by the background
// sealer with the materialized Diff (or an error).
func NewDeferredDiff(cacheKey DiffStoreKey, blockSize int64, inner *utils.SetOnce[Diff]) Diff {
	return &deferredDiff{
		cacheKey:  cacheKey,
		blockSize: blockSize,
		inner:     inner,
	}
}

func (d *deferredDiff) CacheKey() DiffStoreKey {
	return d.cacheKey
}

func (d *deferredDiff) BlockSize() int64 {
	return d.blockSize
}

func (d *deferredDiff) CachePath(ctx context.Context) (string, error) {
	inner, err := d.inner.WaitWithContext(ctx)
	if err != nil {
		return "", err
	}

	return inner.CachePath(ctx)
}

func (d *deferredDiff) ReadAt(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error) {
	inner, err := d.inner.WaitWithContext(ctx)
	if err != nil {
		return 0, err
	}

	return inner.ReadAt(ctx, p, off, ft)
}

func (d *deferredDiff) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	inner, err := d.inner.WaitWithContext(ctx)
	if err != nil {
		return nil, err
	}

	return inner.Slice(ctx, off, length, ft)
}

func (d *deferredDiff) Size(ctx context.Context) (int64, error) {
	inner, err := d.inner.WaitWithContext(ctx)
	if err != nil {
		return 0, err
	}

	return inner.Size(ctx)
}

func (d *deferredDiff) FileSize(ctx context.Context) (int64, error) {
	inner, err := d.inner.WaitWithContext(ctx)
	if err != nil {
		return 0, err
	}

	return inner.FileSize(ctx)
}

// Close waits for the seal to resolve and closes the materialized diff. If the
// seal failed there is nothing to close (the producer cleans up the partial file
// on error), so only close when the diff actually materialized.
func (d *deferredDiff) Close() error {
	if inner, err := d.inner.Wait(); err == nil {
		return inner.Close()
	}

	return nil
}
