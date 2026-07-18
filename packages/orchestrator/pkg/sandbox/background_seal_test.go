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
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// fakeSealProvider is a rootfs.Provider backed by a real block.Overlay so the
// swap/fold/release lifecycle behaves like production. Only the seal-related
// methods are exercised; the rest are unused stubs.
type fakeSealProvider struct {
	overlay   *block.Overlay
	size      int64
	blockSize int64
	dir       string
	gen       int
}

func (f *fakeSealProvider) Start(context.Context) error { return nil }
func (f *fakeSealProvider) Close(context.Context) error { return f.overlay.Close() }
func (f *fakeSealProvider) Path() (string, error)       { return "", nil }
func (f *fakeSealProvider) ExportDiff(context.Context, *os.File, func(context.Context) error) (*header.DiffMetadata, error) {
	return nil, fmt.Errorf("unused")
}

func (f *fakeSealProvider) ExportDiffInPlace(context.Context, *os.File) (*header.DiffMetadata, error) {
	return nil, fmt.Errorf("unused")
}

func (f *fakeSealProvider) SwapForBackgroundSeal(context.Context) (*block.Cache, error) {
	f.gen++
	fresh, err := block.NewCache(f.size, f.blockSize, fmt.Sprintf("%s/fresh%d", f.dir, f.gen), false)
	if err != nil {
		return nil, err
	}

	return f.overlay.SwapCache(fresh)
}

func (f *fakeSealProvider) ReleaseSealed() *block.Cache { return f.overlay.ReleaseSealing() }

func (f *fakeSealProvider) FoldSealed(context.Context) (*block.Cache, error) {
	return f.overlay.FoldSealing()
}

// TestRunBackgroundRootfsSeal verifies the full background lifecycle: the frozen
// cache is sealed into the deferred diff, the diff resolves with the sealed
// bytes, the sealing cache is folded into the writable cache and released, and
// the completion signal fires so a subsequent checkpoint can proceed.
func TestRunBackgroundRootfsSeal(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	numBlocks := int64(4)
	size := blockSize * numBlocks
	base := &fakeRODevice{data: make([]byte, size)}

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

	fake := &fakeSealProvider{overlay: overlay, size: size, blockSize: blockSize, dir: t.TempDir()}
	s := &Sandbox{
		Resources: &Resources{rootfs: fake},
		config:    cfg.BuilderConfig{DefaultCacheDir: t.TempDir()},
	}

	// Swap (VM would be paused here); the returned cache is the frozen old cache.
	sealCache, err := fake.SwapForBackgroundSeal(t.Context())
	require.NoError(t, err)

	// Post-swap write to block 1 (lands in the fresh writable cache).
	blockB := make([]byte, blockSize)
	for i := range blockB {
		blockB[i] = 0xBB
	}
	_, err = overlay.WriteAt(blockB, blockSize)
	require.NoError(t, err)

	buildID := uuid.New()
	diffPromise := utils.NewSetOnce[build.Diff]()
	sealDone := utils.NewSetOnce[struct{}]()

	// Run the seal synchronously (no goroutine) for a deterministic test.
	s.runBackgroundRootfsSeal(t.Context(), sealCache, buildID, blockSize, diffPromise, sealDone)

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

	// The sealing cache was folded into the writable cache and released, so a
	// second swap succeeds (slot freed) and both blocks resolve from the overlay.
	read := func(idx int64) []byte {
		buf := make([]byte, blockSize)
		_, rerr := overlay.ReadAt(t.Context(), buf, idx*blockSize)
		require.NoError(t, rerr)

		return buf
	}
	require.Equal(t, blockA, read(0), "folded pre-swap block still readable")
	require.Equal(t, blockB, read(1), "post-swap block readable")

	c2, err := block.NewCache(size, blockSize, t.TempDir()+"/c2", false)
	require.NoError(t, err)
	_, err = overlay.SwapCache(c2)
	require.NoError(t, err, "sealing slot must be free after fold")

	require.NoError(t, overlay.Close())
}

// fakeRODevice is a minimal block.ReadonlyDevice over a byte buffer.
type fakeRODevice struct {
	data []byte
}

func (f *fakeRODevice) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, nil
	}
	n := copy(p, f.data[off:])

	return n, nil
}

func (f *fakeRODevice) Slice(_ context.Context, off, length int64) ([]byte, error) {
	return f.data[off : off+length], nil
}
func (f *fakeRODevice) Size(context.Context) (int64, error) { return int64(len(f.data)), nil }
func (f *fakeRODevice) Close() error                        { return nil }
func (f *fakeRODevice) BlockSize() int64                    { return int64(header.PageSize) }
func (f *fakeRODevice) Header() *header.Header              { return nil }
func (f *fakeRODevice) SwapHeader(*header.Header)           {}
