package main

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMergeRanges(t *testing.T) {
	t.Parallel()

	require.Nil(t, mergeRanges(nil))

	// Unsorted, overlapping and adjacent ranges coalesce.
	got := mergeRanges([]byteRange{{10, 20}, {0, 5}, {18, 30}, {5, 8}})
	require.Equal(t, []byteRange{{0, 8}, {10, 30}}, got)
}

func TestCoveredBy(t *testing.T) {
	t.Parallel()

	frames := []byteRange{{0, 10}, {10, 20}, {30, 40}}

	require.True(t, coveredBy(frames, byteRange{5, 15}))
	require.True(t, coveredBy(frames, byteRange{0, 20}))
	require.False(t, coveredBy(frames, byteRange{15, 35})) // gap [20, 30)
	require.False(t, coveredBy(frames, byteRange{0, 50}))
}

func TestIntersectsAny(t *testing.T) {
	t.Parallel()

	ranges := []byteRange{{0, 10}, {20, 30}}

	require.True(t, intersectsAny(ranges, byteRange{5, 25}))
	require.True(t, intersectsAny(ranges, byteRange{8, 9}))
	require.False(t, intersectsAny(ranges, byteRange{10, 20})) // exactly the gap
	require.False(t, intersectsAny(ranges, byteRange{30, 40}))
}

func TestValidateFrameTable(t *testing.T) {
	t.Parallel()

	cur := uuid.New()
	ft := storage.NewFullFrameTable(storage.CompressionZstd, []storage.FrameSize{
		{U: 2 * miB, C: 1},
		{U: 2 * miB, C: 1},
	}).Table()
	h := &header.Header{
		Metadata: &header.Metadata{Version: header.MetadataVersionV4, BuildId: cur},
		Mapping: testMapping(t, miB, []header.BuildMap{
			{Offset: 0, Length: 4 * miB, BuildId: cur, BuildStorageOffset: 0},
		}),
		Builds: map[uuid.UUID]header.BuildData{cur: {FrameData: ft}},
	}

	// Frames exactly cover the current build's mappings — no problems.
	require.Empty(t, validateFrameTable(h, ft))

	// Missing self entry in the Builds map.
	noSelf := *h
	noSelf.Builds = map[uuid.UUID]header.BuildData{}
	probs := validateFrameTable(&noSelf, ft)
	require.Len(t, probs, 1)
	require.Contains(t, probs[0], "missing its current build entry")

	// A frame the mappings don't reference (mapping covers only the first).
	shortMap := *h
	shortMap.Mapping = testMapping(t, miB, []header.BuildMap{
		{Offset: 0, Length: 2 * miB, BuildId: cur, BuildStorageOffset: 0},
	})
	probs = validateFrameTable(&shortMap, ft)
	require.NotEmpty(t, probs)
	require.Contains(t, probs[0], "not referenced")
}
