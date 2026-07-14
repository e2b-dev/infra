package header

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGrownRootfsHeader(t *testing.T) {
	t.Parallel()

	const (
		blockSize  = int64(PageSize)
		sourceSize = 4 * blockSize
		grownSize  = 8 * blockSize
	)
	sourceID, grownID := uuid.New(), uuid.New()
	source, err := NewHeader(NewTemplateMetadata(sourceID, uint64(blockSize), uint64(sourceSize)), nil)
	require.NoError(t, err)

	diff := NewDiffMetadata(blockSize, roaring.BitmapOf(4), roaring.BitmapOf(5, 6, 7))
	grown, err := diff.ToResizedDiffHeader(t.Context(), source, grownID, uint64(grownSize))
	require.NoError(t, err)
	require.Equal(t, uint64(sourceSize), source.Metadata.Size)
	require.Equal(t, uint64(grownSize), grown.Metadata.Size)
	require.NoError(t, grown.Mapping.Validate(grown.Metadata.Size, PageSize))

	for offset, buildID := range map[int64]uuid.UUID{
		2 * blockSize: sourceID,
		4 * blockSize: grownID,
		6 * blockSize: uuid.Nil,
	} {
		mapping, err := grown.GetShiftedMapping(t.Context(), offset)
		require.NoError(t, err)
		require.Equal(t, buildID, mapping.BuildId)
	}
}
