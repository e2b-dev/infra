package header

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var (
	ignoreID = uuid.Nil
	baseID   = uuid.New()
	diffID   = uuid.New()
)

var blockSize = uint64(2 << 20)

var size = 8 * blockSize

var simpleBase = []*BuildMap{
	{
		Offset:  0,
		Length:  2 * blockSize,
		BuildId: ignoreID,
	},
	{
		Offset:  2 * blockSize,
		Length:  4 * blockSize,
		BuildId: baseID,
	},
	{
		Offset:  6 * blockSize,
		Length:  2 * blockSize,
		BuildId: ignoreID,
	},
}

func TestMergeMappingsRemoveEmpty(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  0,
			Length:  0,
			BuildId: ignoreID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, simpleBase))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsBaseBeforeDiffNoOverlap(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  7 * blockSize,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, []*BuildMap{
		{
			Offset:  0,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  2 * blockSize,
			Length:  4 * blockSize,
			BuildId: baseID,
		},
		{
			Offset:  6 * blockSize,
			Length:  1 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  7 * blockSize,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsDiffBeforeBaseNoOverlap(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  0,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, []*BuildMap{
		{
			Offset:  0,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
		{
			Offset:  1 * blockSize,
			Length:  1 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  2 * blockSize,
			Length:  4 * blockSize,
			BuildId: baseID,
		},
		{
			Offset:  6 * blockSize,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
	}))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsBaseInsideDiff(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  1 * blockSize,
			Length:  5 * blockSize,
			BuildId: diffID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, []*BuildMap{
		{
			Offset:  0,
			Length:  1 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  1 * blockSize,
			Length:  5 * blockSize,
			BuildId: diffID,
		},
		{
			Offset:  6 * blockSize,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
	}))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsDiffInsideBase(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  3 * blockSize,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, []*BuildMap{
		{
			Offset:  0,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  2 * blockSize,
			Length:  1 * blockSize,
			BuildId: baseID,
		},
		{
			Offset:  3 * blockSize,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
		{
			Offset:  4 * blockSize,
			Length:  2 * blockSize,
			BuildId: baseID,
		},
		{
			Offset:  6 * blockSize,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
	}))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsBaseAfterDiffWithOverlap(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  1 * blockSize,
			Length:  4 * blockSize,
			BuildId: diffID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, []*BuildMap{
		{
			Offset:  0,
			Length:  1 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  1 * blockSize,
			Length:  4 * blockSize,
			BuildId: diffID,
		},
		{
			Offset:  5 * blockSize,
			Length:  1 * blockSize,
			BuildId: baseID,
		},
		{
			Offset:  6 * blockSize,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
	}))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsDiffAfterBaseWithOverlap(t *testing.T) {
	t.Parallel()
	diff := []*BuildMap{
		{
			Offset:  3 * blockSize,
			Length:  4 * blockSize,
			BuildId: diffID,
		},
	}

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, []*BuildMap{
		{
			Offset:  0,
			Length:  2 * blockSize,
			BuildId: ignoreID,
		},
		{
			Offset:  2 * blockSize,
			Length:  1 * blockSize,
			BuildId: baseID,
		},
		{
			Offset:  3 * blockSize,
			Length:  4 * blockSize,
			BuildId: diffID,
		},
		{
			Offset:  7 * blockSize,
			Length:  1 * blockSize,
			BuildId: ignoreID,
		},
	}))

	err := ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestNormalizeMappingsEmptySlice(t *testing.T) {
	t.Parallel()
	m := NormalizeMappings([]*BuildMap{})
	assert.Empty(t, m)
}

func TestNormalizeMappingsSingleMapping(t *testing.T) {
	t.Parallel()
	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 1)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 2*blockSize, m[0].Length)
	assert.Equal(t, baseID, m[0].BuildId)

	err := ValidateMappings(m, 2*blockSize, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsNoAdjacentSameBuildId(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()

	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id2,
			BuildStorageOffset: 0,
		},
		{
			Offset:             4 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id3,
			BuildStorageOffset: 0,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 3)
	assert.Equal(t, id1, m[0].BuildId)
	assert.Equal(t, id2, m[1].BuildId)
	assert.Equal(t, id3, m[2].BuildId)

	err := ValidateMappings(m, 6*blockSize, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsTwoAdjacentSameBuildId(t *testing.T) {
	t.Parallel()
	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             3 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 2 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 1)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 5*blockSize, m[0].Length)
	assert.Equal(t, baseID, m[0].BuildId)
	assert.Equal(t, uint64(0), m[0].BuildStorageOffset)

	err := ValidateMappings(m, 5*blockSize, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsAllSameBuildId(t *testing.T) {
	t.Parallel()
	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 2 * blockSize,
		},
		{
			Offset:             4 * blockSize,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 4 * blockSize,
		},
		{
			Offset:             6 * blockSize,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 6 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 1)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 8*blockSize, m[0].Length)
	assert.Equal(t, baseID, m[0].BuildId)
	assert.Equal(t, uint64(0), m[0].BuildStorageOffset)

	err := ValidateMappings(m, size, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsMultipleGroupsSameBuildId(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	id2 := uuid.New()

	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 2 * blockSize,
		},
		{
			Offset:             4 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id2,
			BuildStorageOffset: 0,
		},
		{
			Offset:             6 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id2,
			BuildStorageOffset: 2 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 2)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 4*blockSize, m[0].Length)
	assert.Equal(t, id1, m[0].BuildId)
	assert.Equal(t, 4*blockSize, m[1].Offset)
	assert.Equal(t, 4*blockSize, m[1].Length)
	assert.Equal(t, id2, m[1].BuildId)

	err := ValidateMappings(m, size, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsAlternatingBuildIds(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	id2 := uuid.New()

	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id2,
			BuildStorageOffset: 0,
		},
		{
			Offset:             4 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 2 * blockSize,
		},
		{
			Offset:             6 * blockSize,
			Length:             2 * blockSize,
			BuildId:            id2,
			BuildStorageOffset: 2 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	// Should not merge any mappings since no adjacent ones have the same BuildId
	assert.Len(t, m, 4)
	assert.Equal(t, id1, m[0].BuildId)
	assert.Equal(t, id2, m[1].BuildId)
	assert.Equal(t, id1, m[2].BuildId)
	assert.Equal(t, id2, m[3].BuildId)

	err := ValidateMappings(m, size, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsThreeConsecutiveSameBuildId(t *testing.T) {
	t.Parallel()
	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             3 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 2 * blockSize,
		},
		{
			Offset:             5 * blockSize,
			Length:             1 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 5 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 1)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 6*blockSize, m[0].Length)
	assert.Equal(t, baseID, m[0].BuildId)
	assert.Equal(t, uint64(0), m[0].BuildStorageOffset)

	err := ValidateMappings(m, 6*blockSize, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsMixedPattern(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()

	input := []*BuildMap{
		{
			Offset:             0,
			Length:             1 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 0,
		},
		{
			Offset:             1 * blockSize,
			Length:             1 * blockSize,
			BuildId:            id1,
			BuildStorageOffset: 1 * blockSize,
		},
		{
			Offset:             2 * blockSize,
			Length:             1 * blockSize,
			BuildId:            id2,
			BuildStorageOffset: 0,
		},
		{
			Offset:             3 * blockSize,
			Length:             1 * blockSize,
			BuildId:            id3,
			BuildStorageOffset: 0,
		},
		{
			Offset:             4 * blockSize,
			Length:             1 * blockSize,
			BuildId:            id3,
			BuildStorageOffset: 1 * blockSize,
		},
		{
			Offset:             5 * blockSize,
			Length:             1 * blockSize,
			BuildId:            id3,
			BuildStorageOffset: 2 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	assert.Len(t, m, 3)
	// First two merged
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 2*blockSize, m[0].Length)
	assert.Equal(t, id1, m[0].BuildId)
	// Middle one stays alone
	assert.Equal(t, 2*blockSize, m[1].Offset)
	assert.Equal(t, 1*blockSize, m[1].Length)
	assert.Equal(t, id2, m[1].BuildId)
	// Last three merged
	assert.Equal(t, 3*blockSize, m[2].Offset)
	assert.Equal(t, 3*blockSize, m[2].Length)
	assert.Equal(t, id3, m[2].BuildId)

	err := ValidateMappings(m, 6*blockSize, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsZeroLengthMapping(t *testing.T) {
	t.Parallel()
	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             0,
			BuildId:            baseID,
			BuildStorageOffset: 2 * blockSize,
		},
		{
			Offset:             2 * blockSize,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 2 * blockSize,
		},
	}

	m := NormalizeMappings(input)

	// All should be merged since they all have the same BuildId
	assert.Len(t, m, 1)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 4*blockSize, m[0].Length)
	assert.Equal(t, baseID, m[0].BuildId)

	err := ValidateMappings(m, 4*blockSize, blockSize)
	require.NoError(t, err)
}

func TestNormalizeMappingsDoesNotModifyInput(t *testing.T) {
	t.Parallel()
	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
		{
			Offset:             2 * blockSize,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 2 * blockSize,
		},
		{
			Offset:             4 * blockSize,
			Length:             2 * blockSize,
			BuildId:            diffID,
			BuildStorageOffset: 0,
		},
	}

	// Store original values
	originalLen := len(input)
	originalOffset0 := input[0].Offset
	originalLength0 := input[0].Length
	originalOffset1 := input[1].Offset
	originalLength1 := input[1].Length

	m := NormalizeMappings(input)

	// Verify result is correct
	assert.Len(t, m, 2)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, 4*blockSize, m[0].Length)

	// Verify input was not modified
	assert.Len(t, input, originalLen, "Input slice length should not change")
	assert.Equal(t, originalOffset0, input[0].Offset, "Input[0].Offset should not change")
	assert.Equal(t, originalLength0, input[0].Length, "Input[0].Length should not change")
	assert.Equal(t, originalOffset1, input[1].Offset, "Input[1].Offset should not change")
	assert.Equal(t, originalLength1, input[1].Length, "Input[1].Length should not change")

	err := ValidateMappings(m, 6*blockSize, blockSize)
	require.NoError(t, err)
}

// =============================================================================
// Header.AddFrames Tests
// =============================================================================

func TestHeader_AddFrames_SingleBuild(t *testing.T) {
	t.Parallel()
	buildId := uuid.New()

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x600000, C: 0x200000},
			{U: 0x400000, C: 0x180000},
		},
	}

	mappings := []*BuildMap{
		{Offset: 0x0, Length: 0x100000, BuildId: buildId, BuildStorageOffset: 0x0},
		{Offset: 0x100000, Length: 0x200000, BuildId: buildId, BuildStorageOffset: 0x100000},
		{Offset: 0x300000, Length: 0x300000, BuildId: buildId, BuildStorageOffset: 0x500000},
	}

	h, err := NewHeader(&Metadata{BuildId: buildId, Size: 0x600000, BlockSize: 0x1000}, mappings)
	require.NoError(t, err)

	require.NoError(t, h.AddFrames(frameTable))

	require.NotNil(t, h.Mapping[0].FrameTable)
	assert.Len(t, h.Mapping[0].FrameTable.Frames, 1)
	assert.Equal(t, int64(0), h.Mapping[0].FrameTable.StartAt.U)

	require.NotNil(t, h.Mapping[1].FrameTable)
	assert.Len(t, h.Mapping[1].FrameTable.Frames, 1)

	require.NotNil(t, h.Mapping[2].FrameTable)
	assert.Len(t, h.Mapping[2].FrameTable.Frames, 2, "mapping spanning frame boundary should include both frames")
}

func TestHeader_AddFrames_TemplateInheritance(t *testing.T) {
	t.Parallel()
	parentId := uuid.New()
	childId := uuid.New()

	childFrameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x150000},
			{U: 0x400000, C: 0x160000},
		},
	}

	mappings := []*BuildMap{
		{Offset: 0x0, Length: 0x1000000, BuildId: parentId, BuildStorageOffset: 0x0},
		{Offset: 0x1000000, Length: 0x100000, BuildId: childId, BuildStorageOffset: 0x0},
		{Offset: 0x1100000, Length: 0x200000, BuildId: childId, BuildStorageOffset: 0x100000},
		{Offset: 0x1300000, Length: 0x500000, BuildId: parentId, BuildStorageOffset: 0x1000000},
		{Offset: 0x1800000, Length: 0x1000, BuildId: childId, BuildStorageOffset: 0x300000},
	}

	h, err := NewHeader(&Metadata{BuildId: childId, Size: 0x1801000, BlockSize: 0x1000}, mappings)
	require.NoError(t, err)

	require.NoError(t, h.AddFrames(childFrameTable))

	assert.Nil(t, h.Mapping[0].FrameTable, "parent mapping should not be modified")
	assert.Nil(t, h.Mapping[3].FrameTable, "parent mapping should not be modified")
	require.NotNil(t, h.Mapping[1].FrameTable, "child mapping should have frame table")
	require.NotNil(t, h.Mapping[2].FrameTable, "child mapping should have frame table")
	require.NotNil(t, h.Mapping[4].FrameTable, "child mapping should have frame table")
	assert.Len(t, h.Mapping[4].FrameTable.Frames, 1)
}

func TestBuildMap_AddFrames_OffsetVsBuildStorageOffset(t *testing.T) {
	t.Parallel()
	buildId := uuid.New()

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x100000, C: 0x50000},
		},
	}

	mappings := []*BuildMap{
		{Offset: 0xc1a000, Length: 0x1000, BuildId: buildId, BuildStorageOffset: 0x33000},
		{Offset: 0x80000000, Length: 0x8000, BuildId: buildId, BuildStorageOffset: 0x50000},
	}

	for _, m := range mappings {
		require.NoError(t, m.AddFrames(frameTable))
	}

	require.NotNil(t, mappings[0].FrameTable)
	require.NotNil(t, mappings[1].FrameTable)
}

func TestBuildMap_AddFrames_MappingBeyondFrameTable(t *testing.T) {
	t.Parallel()
	buildId := uuid.New()

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x100000, C: 0x50000},
		},
	}

	m := &BuildMap{
		Offset:             0x1000000,
		Length:             0x1000,
		BuildId:            buildId,
		BuildStorageOffset: 0x200000,
	}

	err := m.AddFrames(frameTable)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage offset 0x200000")
}

func TestBuildMap_AddFrames_NilFrameTable(t *testing.T) {
	t.Parallel()
	buildId := uuid.New()

	m := &BuildMap{Offset: 0, Length: 0x1000, BuildId: buildId, BuildStorageOffset: 0}
	err := m.AddFrames(nil)
	require.NoError(t, err)
	assert.Nil(t, m.FrameTable)
}

func TestBuildMap_AddFrames_FrameBoundaries(t *testing.T) {
	t.Parallel()
	buildId := uuid.New()

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x100000},
			{U: 0x400000, C: 0x100000},
			{U: 0x400000, C: 0x100000},
		},
	}

	mappings := []*BuildMap{
		{Offset: 0x1000000, Length: 0x1000, BuildId: buildId, BuildStorageOffset: 0x0},
		{Offset: 0x2000000, Length: 0x1000, BuildId: buildId, BuildStorageOffset: 0x400000},
		{Offset: 0x3000000, Length: 0x100000, BuildId: buildId, BuildStorageOffset: 0x380000},
		{Offset: 0x4000000, Length: 0x800000, BuildId: buildId, BuildStorageOffset: 0x200000},
		{Offset: 0x5000000, Length: 0x1000, BuildId: buildId, BuildStorageOffset: 0xbff000},
	}

	for _, m := range mappings {
		require.NoError(t, m.AddFrames(frameTable))
	}

	assert.Len(t, mappings[0].FrameTable.Frames, 1, "mapping at frame start")
	assert.Len(t, mappings[1].FrameTable.Frames, 1, "mapping at frame boundary")
	assert.Len(t, mappings[2].FrameTable.Frames, 2, "mapping spanning 2 frames")
	assert.Len(t, mappings[3].FrameTable.Frames, 3, "mapping spanning 3 frames")
	assert.Len(t, mappings[4].FrameTable.Frames, 1, "mapping at end of last frame")
}

func TestHeader_AddFrames_SparseModifications(t *testing.T) {
	t.Parallel()
	parentId := uuid.New()
	childId := uuid.New()

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x180000},
			{U: 0x400000, C: 0x1a0000},
		},
	}

	mappings := []*BuildMap{
		{Offset: 0x0, Length: 0x400000, BuildId: parentId, BuildStorageOffset: 0x0},
		{Offset: 0x400000, Length: 0x1000, BuildId: childId, BuildStorageOffset: 0x0},
		{Offset: 0x401000, Length: 0x1000, BuildId: childId, BuildStorageOffset: 0x1000},
		{Offset: 0x402000, Length: 0x1000, BuildId: childId, BuildStorageOffset: 0x2000},
		{Offset: 0x403000, Length: 0x10000, BuildId: childId, BuildStorageOffset: 0x3000},
		{Offset: 0x413000, Length: 0x3a0000, BuildId: childId, BuildStorageOffset: 0x3e0000},
		{Offset: 0x7b3000, Length: 0x1000000, BuildId: parentId, BuildStorageOffset: 0x400000},
	}

	h, err := NewHeader(&Metadata{BuildId: childId, Size: 0x17b3000, BlockSize: 0x1000}, mappings)
	require.NoError(t, err)

	require.NoError(t, h.AddFrames(frameTable))

	assert.Nil(t, h.Mapping[0].FrameTable)
	assert.Nil(t, h.Mapping[6].FrameTable)

	for i := 1; i <= 5; i++ {
		require.NotNil(t, h.Mapping[i].FrameTable, "child mapping %d should have frame table", i)
	}

	assert.Len(t, h.Mapping[5].FrameTable.Frames, 2, "large chunk should span multiple frames")
}

// =============================================================================
// NormalizeMappings with FrameTable Tests
// =============================================================================

func TestNormalizeMappingsWithFrameTable_SingleMapping(t *testing.T) {
	t.Parallel()
	ft := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x100000},
		},
	}

	input := []*BuildMap{
		{
			Offset:             0,
			Length:             2 * blockSize,
			BuildId:            baseID,
			BuildStorageOffset: 0,
			FrameTable:         ft,
		},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 1)
	require.NotNil(t, m[0].FrameTable, "FrameTable should be preserved")
	assert.Equal(t, storage.CompressionZstd, m[0].FrameTable.CompressionType)
	assert.Len(t, m[0].FrameTable.Frames, 1)
}

func TestNormalizeMappingsWithFrameTable_MergeAdjacentWithSameFrameTable(t *testing.T) {
	t.Parallel()
	// Two adjacent mappings with the same BuildId and FrameTables covering different frames
	ft1 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x100000},
		},
	}
	ft2 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0x400000, C: 0x100000},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x120000},
		},
	}

	input := []*BuildMap{
		{
			Offset:             0,
			Length:             0x400000,
			BuildId:            baseID,
			BuildStorageOffset: 0,
			FrameTable:         ft1,
		},
		{
			Offset:             0x400000,
			Length:             0x400000,
			BuildId:            baseID,
			BuildStorageOffset: 0x400000,
			FrameTable:         ft2,
		},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 1, "adjacent mappings with same BuildId should merge")
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, uint64(0x800000), m[0].Length)
	require.NotNil(t, m[0].FrameTable, "merged mapping should have FrameTable")
	assert.Len(t, m[0].FrameTable.Frames, 2, "merged FrameTable should have frames from both")
	assert.Equal(t, int64(0), m[0].FrameTable.StartAt.U)
	assert.Equal(t, int64(0), m[0].FrameTable.StartAt.C)
}

func TestNormalizeMappingsWithFrameTable_MergeThreeAdjacent(t *testing.T) {
	t.Parallel()
	ft1 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}
	ft2 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0x400000, C: 0x100000},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x110000}},
	}
	ft3 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0x800000, C: 0x210000},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x120000}},
	}

	input := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: ft1},
		{Offset: 0x400000, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0x400000, FrameTable: ft2},
		{Offset: 0x800000, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0x800000, FrameTable: ft3},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 1)
	assert.Equal(t, uint64(0xc00000), m[0].Length)
	require.NotNil(t, m[0].FrameTable)
	assert.Len(t, m[0].FrameTable.Frames, 3, "should have all three frames")
}

func TestNormalizeMappingsWithFrameTable_MixedWithAndWithoutFrameTable(t *testing.T) {
	t.Parallel()
	ft := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}

	// First mapping has FrameTable, second doesn't (same BuildId)
	input := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: ft},
		{Offset: 0x400000, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0x400000, FrameTable: nil},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 1)
	require.NotNil(t, m[0].FrameTable, "should keep FrameTable from first mapping")
	assert.Len(t, m[0].FrameTable.Frames, 1)
}

func TestNormalizeMappingsWithFrameTable_SecondHasFrameTableFirstDoesNot(t *testing.T) {
	t.Parallel()
	ft := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0x400000, C: 0x100000},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}

	// First mapping has no FrameTable, second does (same BuildId)
	input := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: nil},
		{Offset: 0x400000, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0x400000, FrameTable: ft},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 1)
	require.NotNil(t, m[0].FrameTable, "should take FrameTable from second mapping")
	assert.Equal(t, int64(0x400000), m[0].FrameTable.StartAt.U)
}

func TestNormalizeMappingsWithFrameTable_DifferentBuildIds(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	id2 := uuid.New()

	ft1 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}
	ft2 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x120000}},
	}

	input := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: id1, BuildStorageOffset: 0, FrameTable: ft1},
		{Offset: 0x400000, Length: 0x400000, BuildId: id2, BuildStorageOffset: 0, FrameTable: ft2},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 2, "different BuildIds should not merge")
	require.NotNil(t, m[0].FrameTable)
	require.NotNil(t, m[1].FrameTable)
	assert.Equal(t, id1, m[0].BuildId)
	assert.Equal(t, id2, m[1].BuildId)
}

func TestNormalizeMappingsWithFrameTable_OverlappingFrames(t *testing.T) {
	t.Parallel()
	// Two mappings that share the same frame (subsets of the same frame)
	ft1 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x800000, C: 0x200000}},
	}
	ft2 := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x800000, C: 0x200000}},
	}

	input := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: ft1},
		{Offset: 0x400000, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0x400000, FrameTable: ft2},
	}

	m := NormalizeMappings(input)

	require.Len(t, m, 1)
	require.NotNil(t, m[0].FrameTable)
	// The overlapping frame should appear only once
	assert.Len(t, m[0].FrameTable.Frames, 1, "overlapping frame should not be duplicated")
}

func TestNormalizeMappingsWithFrameTable_DoesNotModifyInput(t *testing.T) {
	t.Parallel()
	ft := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}

	input := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: ft},
		{Offset: 0x400000, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0x400000, FrameTable: nil},
	}

	originalFT := input[0].FrameTable

	m := NormalizeMappings(input)

	// Verify input was not modified
	assert.Equal(t, originalFT, input[0].FrameTable, "input FrameTable should not be modified")
	assert.Nil(t, input[1].FrameTable, "input[1].FrameTable should remain nil")
	// Verify result is independent
	require.Len(t, m, 1)
	require.NotNil(t, m[0].FrameTable)
}

// =============================================================================
// MergeMappings with FrameTable Tests
// =============================================================================

func TestMergeMappingsWithFrameTable_DiffSplitsBaseWithFrameTable(t *testing.T) {
	t.Parallel()
	// Base has a FrameTable, diff splits it
	baseFT := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 0x400000, C: 0x100000},
			{U: 0x400000, C: 0x110000},
		},
	}

	base := []*BuildMap{
		{
			Offset:             0,
			Length:             0x800000,
			BuildId:            baseID,
			BuildStorageOffset: 0,
			FrameTable:         baseFT,
		},
	}

	diff := []*BuildMap{
		{
			Offset:             0x200000,
			Length:             0x200000,
			BuildId:            diffID,
			BuildStorageOffset: 0,
			FrameTable:         nil, // diff is uncompressed
		},
	}

	m := MergeMappings(base, diff)

	require.Len(t, m, 3)

	// Left part of base (before diff)
	assert.Equal(t, uint64(0), m[0].Offset)
	assert.Equal(t, uint64(0x200000), m[0].Length)
	assert.Equal(t, baseID, m[0].BuildId)
	// FrameTable should be subset for left part

	// Diff in the middle
	assert.Equal(t, uint64(0x200000), m[1].Offset)
	assert.Equal(t, uint64(0x200000), m[1].Length)
	assert.Equal(t, diffID, m[1].BuildId)

	// Right part of base (after diff)
	assert.Equal(t, uint64(0x400000), m[2].Offset)
	assert.Equal(t, uint64(0x400000), m[2].Length)
	assert.Equal(t, baseID, m[2].BuildId)
}

func TestMergeMappingsWithFrameTable_PreservesFrameTableOnNoOverlap(t *testing.T) {
	t.Parallel()
	baseFT := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}

	base := []*BuildMap{
		{Offset: 0, Length: 0x400000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: baseFT},
	}

	diff := []*BuildMap{
		{Offset: 0x400000, Length: 0x200000, BuildId: diffID, BuildStorageOffset: 0, FrameTable: nil},
	}

	m := MergeMappings(base, diff)

	require.Len(t, m, 2)
	require.NotNil(t, m[0].FrameTable, "base FrameTable should be preserved when no overlap")
	assert.Equal(t, baseFT.CompressionType, m[0].FrameTable.CompressionType)
}

func TestMergeMappingsWithFrameTable_DiffCompletelyCoversBase(t *testing.T) {
	t.Parallel()
	baseFT := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}
	diffFT := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: 0x800000, C: 0x200000}},
	}

	base := []*BuildMap{
		{Offset: 0x100000, Length: 0x200000, BuildId: baseID, BuildStorageOffset: 0, FrameTable: baseFT},
	}

	diff := []*BuildMap{
		{Offset: 0, Length: 0x800000, BuildId: diffID, BuildStorageOffset: 0, FrameTable: diffFT},
	}

	m := MergeMappings(base, diff)

	require.Len(t, m, 1)
	assert.Equal(t, diffID, m[0].BuildId)
	require.NotNil(t, m[0].FrameTable, "diff's FrameTable should be used")
	assert.Equal(t, diffFT.StartAt, m[0].FrameTable.StartAt)
}

func TestBuildMapCopy_PreservesFrameTable(t *testing.T) {
	t.Parallel()
	ft := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0x100, C: 0x50},
		Frames:          []storage.FrameSize{{U: 0x400000, C: 0x100000}},
	}

	original := &BuildMap{
		Offset:             0x1000,
		Length:             0x2000,
		BuildId:            baseID,
		BuildStorageOffset: 0x500,
		FrameTable:         ft,
	}

	copied := original.Copy()

	assert.Equal(t, original.Offset, copied.Offset)
	assert.Equal(t, original.Length, copied.Length)
	assert.Equal(t, original.BuildId, copied.BuildId)
	assert.Equal(t, original.BuildStorageOffset, copied.BuildStorageOffset)
	assert.Equal(t, original.FrameTable, copied.FrameTable, "FrameTable pointer should be copied")
}

func TestBuildMapCopy_NilFrameTable(t *testing.T) {
	t.Parallel()
	original := &BuildMap{
		Offset:             0x1000,
		Length:             0x2000,
		BuildId:            baseID,
		BuildStorageOffset: 0x500,
		FrameTable:         nil,
	}

	copied := original.Copy()

	assert.Nil(t, copied.FrameTable)
}
