package header

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestNewHeader_PageGranularMappingUnderHugepageBlockSize reproduces the
// production case: a memory file has BlockSize = 2 MiB (hugepage) but its diff
// mappings are page-granular (4 KiB), so the header carries 4 KiB-aligned
// offsets under a 2 MiB block size. The compact mapping must encode in PageSize
// units, not metadata.BlockSize, or header construction fails and bricks every
// sandbox create/resume.
func TestNewHeader_PageGranularMappingUnderHugepageBlockSize(t *testing.T) {
	t.Parallel()

	const hugepage = uint64(2 << 20) // 2 MiB memory block size
	a := uuid.New()
	b := uuid.New()

	// Page-granular (4 KiB) mappings, NOT aligned to the 2 MiB block size.
	mappings := []BuildMap{
		{Offset: 0, Length: PageSize, BuildId: a, BuildStorageOffset: 0},
		{Offset: PageSize, Length: PageSize, BuildId: b, BuildStorageOffset: 0},
		{Offset: 2 * PageSize, Length: 2 * PageSize, BuildId: a, BuildStorageOffset: PageSize},
	}
	meta := &Metadata{Version: MetadataVersionV4, BlockSize: hugepage, Size: 4 * PageSize, BuildId: a, BaseBuildId: b}

	h, err := NewHeader(meta, mappings)
	require.NoError(t, err, "page-granular mappings under a hugepage block size must be accepted")
	require.True(t, Equal(mappings, h.Mapping.Slice()))

	// And the offset lookup must resolve correctly at page granularity.
	m, err := h.GetShiftedMapping(t.Context(), PageSize)
	require.NoError(t, err)
	require.Equal(t, b, m.BuildId)
}

func TestNewMapping_RoundTrip(t *testing.T) {
	t.Parallel()

	bs := uint64(4096)
	a := uuid.New()
	b := uuid.New()
	src := []BuildMap{
		{Offset: 0, Length: 2 * bs, BuildId: a, BuildStorageOffset: 0},
		{Offset: 2 * bs, Length: bs, BuildId: b, BuildStorageOffset: 0},
		{Offset: 3 * bs, Length: bs, BuildId: a, BuildStorageOffset: 2 * bs},
	}

	m, err := NewMapping(bs, src)
	require.NoError(t, err)
	require.Equal(t, len(src), m.Len())

	require.True(t, Equal(src, m.Slice()), "Slice must round-trip the input")
	for i, want := range src {
		require.Equal(t, want, m.At(i), "At(%d)", i)
	}

	// Builds deduplicated to {a, b}.
	require.ElementsMatch(t, []uuid.UUID{a, b}, m.Builds())
}

func TestNewMapping_RejectsUnaligned(t *testing.T) {
	t.Parallel()

	bs := uint64(4096)
	id := uuid.New()

	_, err := NewMapping(bs, []BuildMap{{Offset: 123, Length: bs, BuildId: id}})
	require.ErrorContains(t, err, "offset")

	_, err = NewMapping(bs, []BuildMap{{Offset: 0, Length: 123, BuildId: id}})
	require.ErrorContains(t, err, "length")

	_, err = NewMapping(bs, []BuildMap{{Offset: 0, Length: bs, BuildId: id, BuildStorageOffset: 123}})
	require.ErrorContains(t, err, "build storage offset")

	_, err = NewMapping(0, []BuildMap{{Offset: 0, Length: bs, BuildId: id}})
	require.ErrorContains(t, err, "block size")
}

func TestMapping_SearchOffset(t *testing.T) {
	t.Parallel()

	bs := uint64(4096)
	id := uuid.New()
	src := []BuildMap{
		{Offset: 0, Length: 2 * bs, BuildId: id},
		{Offset: 2 * bs, Length: 2 * bs, BuildId: id, BuildStorageOffset: 2 * bs},
		{Offset: 4 * bs, Length: bs, BuildId: id, BuildStorageOffset: 4 * bs},
	}
	m, err := NewMapping(bs, src)
	require.NoError(t, err)

	// SearchOffset must match sort.Search over the materialized offsets for
	// every page within range, including non-block-aligned probes.
	for off := int64(0); off < int64(5*bs); off += int64(PageSize) {
		want := 0
		for _, bm := range src {
			if int64(bm.Offset) > off {
				break
			}
			want++
		}
		require.Equal(t, want, m.SearchOffset(off), "off=%d", off)
	}

	require.Equal(t, 0, m.SearchOffset(-1))
}

func TestMapping_Validate(t *testing.T) {
	t.Parallel()

	bs := uint64(4096)
	id := uuid.New()
	size := 4 * bs
	m, err := NewMapping(bs, []BuildMap{
		{Offset: 0, Length: 2 * bs, BuildId: id},
		{Offset: 2 * bs, Length: 2 * bs, BuildId: id, BuildStorageOffset: 2 * bs},
	})
	require.NoError(t, err)
	require.NoError(t, m.Validate(size, bs))

	// Wrong size is rejected.
	require.Error(t, m.Validate(size+bs, bs))

	// A gap is rejected.
	gap, err := NewMapping(bs, []BuildMap{
		{Offset: 0, Length: bs, BuildId: id},
		{Offset: 2 * bs, Length: bs, BuildId: id, BuildStorageOffset: 2 * bs},
	})
	require.NoError(t, err)
	require.Error(t, gap.Validate(3*bs, bs))
}
