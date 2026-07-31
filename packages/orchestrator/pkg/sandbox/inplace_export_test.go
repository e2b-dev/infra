//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// fakeInPlaceRootfs is a rootfs.Provider backed by a real block.Overlay so the
// swap/fold lifecycle behaves like production. Only the in-place seal methods are
// exercised; everything else nil-panics via the embedded interface (unused here).
type fakeInPlaceRootfs struct {
	rootfs.Provider

	overlay   *block.Overlay
	size      int64
	blockSize int64
	dir       string
	gen       int
}

func (f *fakeInPlaceRootfs) SwapForBackgroundSeal(context.Context) (*block.Cache, error) {
	f.gen++
	fresh, err := block.NewCache(f.size, f.blockSize, fmt.Sprintf("%s/fresh%d", f.dir, f.gen), false)
	if err != nil {
		return nil, err
	}

	return f.overlay.SwapCache(fresh)
}

func (f *fakeInPlaceRootfs) FoldSealed(context.Context) (*block.Cache, error) {
	return f.overlay.FoldSealing()
}

// inPlaceRODevice is a minimal block.ReadonlyDevice over a fixed byte buffer.
type inPlaceRODevice struct{ data []byte }

func (d *inPlaceRODevice) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	if off >= int64(len(d.data)) {
		return 0, nil
	}

	return copy(p, d.data[off:]), nil
}

func (d *inPlaceRODevice) Slice(_ context.Context, off, length int64) ([]byte, error) {
	return d.data[off : off+length], nil
}
func (d *inPlaceRODevice) Size(context.Context) (int64, error) { return int64(len(d.data)), nil }
func (d *inPlaceRODevice) Close() error                        { return nil }
func (d *inPlaceRODevice) BlockSize() int64                    { return int64(header.PageSize) }
func (d *inPlaceRODevice) Header() *header.Header              { return nil }
func (d *inPlaceRODevice) SwapHeader(*header.Header)           {}

// TestRunInPlaceRootfsExport verifies the in-place background lifecycle: the
// frozen (swapped-out) cache is sealed into the deferred diff, the diff resolves
// with the pre-swap bytes, the sealing cache is folded into the live writable
// cache and released, and the completion signal fires so a subsequent checkpoint
// can swap again.
func TestRunInPlaceRootfsExport(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	numBlocks := int64(4)
	size := blockSize * numBlocks
	base := &inPlaceRODevice{data: make([]byte, size)}

	c0, err := block.NewCache(size, blockSize, t.TempDir()+"/c0", false)
	require.NoError(t, err)
	overlay := block.NewOverlay(base, c0)

	// Pre-checkpoint write to block 0.
	blockA := make([]byte, blockSize)
	for i := range blockA {
		blockA[i] = 0xAA
	}
	_, err = overlay.WriteAt(blockA, 0)
	require.NoError(t, err)

	fake := &fakeInPlaceRootfs{overlay: overlay, size: size, blockSize: blockSize, dir: t.TempDir()}
	s := &Sandbox{
		Resources: &Resources{rootfs: fake},
		config:    cfg.BuilderConfig{DefaultCacheDir: t.TempDir()},
	}

	// Swap (VM would be paused here); the returned cache is the frozen old cache.
	sealCache, err := fake.SwapForBackgroundSeal(t.Context())
	require.NoError(t, err)

	// Post-swap write to block 1 lands in the fresh writable cache.
	blockB := make([]byte, blockSize)
	for i := range blockB {
		blockB[i] = 0xBB
	}
	_, err = overlay.WriteAt(blockB, blockSize)
	require.NoError(t, err)

	meta, err := sealCache.DiffMetadata()
	require.NoError(t, err)

	buildID := uuid.New()
	diffPromise := utils.NewSetOnce[build.Diff]()
	sealDone := utils.NewSetOnce[struct{}]()

	// Run the seal synchronously (no goroutine) for a deterministic test.
	s.runInPlaceRootfsExport(t.Context(), sealCache, buildID, blockSize, meta, diffPromise, sealDone)

	// The completion signal fired.
	_, err = sealDone.Result()
	require.NoError(t, err)

	// The deferred diff resolved to the sealed bytes (block 0 = 0xAA).
	diff, err := diffPromise.Result()
	require.NoError(t, err)
	path, err := diff.CachePath(t.Context())
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, blockA, got, "sealed diff must contain the pre-swap block")
	require.NoError(t, diff.Close())

	// The sealing cache was folded into the writable cache and released, so both
	// blocks resolve from the overlay and a second swap succeeds (slot freed).
	read := func(idx int64) []byte {
		buf := make([]byte, blockSize)
		_, rerr := overlay.ReadAt(t.Context(), buf, idx*blockSize)
		require.NoError(t, rerr)

		return buf
	}
	require.Equal(t, blockA, read(0), "folded pre-swap block still readable")
	require.Equal(t, blockB, read(1), "post-swap block readable")

	_, err = fake.SwapForBackgroundSeal(t.Context())
	require.NoError(t, err, "sealing slot must be free after fold")

	require.NoError(t, overlay.Close())
}
