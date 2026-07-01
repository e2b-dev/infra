package header

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ToProvisionalDiffHeader attributes every dirty page to the new build with an
// identity storage offset (= its device offset), and leaves clean pages mapped
// to the parent — all without any dedup metadata.
func TestToProvisionalDiffHeader_IdentityOffsetsOverParent(t *testing.T) {
	t.Parallel()

	const numPages = 6
	parent := uuid.New()
	child := uuid.New()

	base, err := NewHeader(
		&Metadata{Version: MetadataVersionV4, BlockSize: PageSize, Size: numPages * PageSize, BuildId: parent, BaseBuildId: parent},
		[]BuildMap{{Offset: 0, Length: numPages * PageSize, BuildId: parent, BuildStorageOffset: 0}},
	)
	require.NoError(t, err)

	dirty := roaring.New()
	dirty.AddMany([]uint32{1, 3})
	dm := &DiffMetadata{Dirty: dirty, Empty: roaring.New(), BlockSize: PageSize}

	h, err := dm.ToProvisionalDiffHeader(t.Context(), base, child)
	require.NoError(t, err)

	// Dirty pages resolve to the child build at an identity storage offset.
	for _, page := range []int64{1, 3} {
		m, err := h.GetShiftedMapping(t.Context(), page*PageSize)
		require.NoError(t, err)
		require.Equal(t, child, m.BuildId, "page %d should map to child", page)
		require.Equal(t, uint64(page*PageSize), m.Offset, "page %d storage offset must be identity", page)
	}

	// Clean pages still resolve to the parent.
	for _, page := range []int64{0, 2, 4, 5} {
		m, err := h.GetShiftedMapping(t.Context(), page*PageSize)
		require.NoError(t, err)
		require.Equal(t, parent, m.BuildId, "page %d should map to parent", page)
	}
}
