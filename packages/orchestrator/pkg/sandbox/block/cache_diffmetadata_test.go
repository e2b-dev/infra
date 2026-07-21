//go:build linux

package block

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestCacheDiffMetadata_MatchesExportToDiff verifies DiffMetadata (used to build
// the diff header synchronously) produces the same dirty/empty bitmaps as the
// metadata ExportToDiff computes while copying, so a background seal's header is
// exact.
func TestCacheDiffMetadata_MatchesExportToDiff(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	numBlocks := int64(4)

	c, err := NewCache(blockSize*numBlocks, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	// Dirty block 0, zero block 2, leave 1 and 3 untouched.
	dirtyBlock := make([]byte, blockSize)
	for i := range dirtyBlock {
		dirtyBlock[i] = 0xAB
	}
	_, err = c.WriteAt(dirtyBlock, 0)
	require.NoError(t, err)
	_, err = c.WriteZeroesAt(2*blockSize, blockSize)
	require.NoError(t, err)

	// Metadata read without copying.
	meta, err := c.DiffMetadata()
	require.NoError(t, err)

	// Metadata computed by the actual export.
	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = out.Close() })

	exportMeta, err := c.ExportToDiff(t.Context(), out)
	require.NoError(t, err)

	require.True(t, meta.Dirty.Equals(exportMeta.Dirty), "dirty bitmaps must match")
	require.True(t, meta.Empty.Equals(exportMeta.Empty), "empty bitmaps must match")
	require.Equal(t, exportMeta.BlockSize, meta.BlockSize)
	require.EqualValues(t, 1, meta.Dirty.GetCardinality())
	require.EqualValues(t, 1, meta.Empty.GetCardinality())
}
