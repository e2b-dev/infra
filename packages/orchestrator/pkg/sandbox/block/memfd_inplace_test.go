//go:build linux

package block

import (
	"os"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TestNewCacheFromMemfdAsync_KeepMemfdOpen verifies that with closeMemfd=false
// the async copy does NOT close the memfd, so an in-place resume can keep using
// it. A successful Close afterwards proves it was still open (a double-close
// would error).
func TestNewCacheFromMemfdAsync_KeepMemfdOpen(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	numPages := uint32(8)
	memfd, expected := newTestMemfd(t, pageSize*int64(numPages))

	dirty := roaring.New()
	dirty.AddRange(0, uint64(numPages))

	cachePath := t.TempDir() + "/cache"
	cache, err := NewCacheFromMemfdAsync(t.Context(), pageSize, cachePath, memfd, dirty, false /* closeMemfd */)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.NoError(t, cache.Wait(t.Context()))

	// The copy is still correct.
	fromFile, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Equal(t, expected, fromFile)

	// The memfd was left open — closing it now succeeds exactly once.
	require.NoError(t, memfd.Close())
}

// TestNewCacheFromMemfdDeduped_KeepMemfdOpen is the dedup-path counterpart: the
// dedup goroutine reads the memfd too, and with closeMemfd=false it must leave
// it open.
func TestNewCacheFromMemfdDeduped_KeepMemfdOpen(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	numPages := uint32(8)
	size := pageSize * int64(numPages)
	memfd, _ := newTestMemfd(t, size)

	dirty := roaring.New()
	dirty.AddRange(0, uint64(numPages))

	metaOut := utils.NewSetOnce[*header.DiffMetadata]()
	cache, err := NewCacheFromMemfdDeduped(
		t.Context(), &fakeOriginalDevice{data: make([]byte, size)}, pageSize, t.TempDir()+"/dedup",
		memfd, dirty, false, false, DedupBudget{}, nil, metaOut, false, /* closeMemfd */
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	_, err = cache.Wait(t.Context())
	require.NoError(t, err)

	// The memfd was left open — closing it now succeeds exactly once.
	require.NoError(t, memfd.Close())
}
