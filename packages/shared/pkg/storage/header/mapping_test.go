package header

import (
	"fmt"
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

var simpleBase = []BuildMap{
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
	diff := []BuildMap{
		{
			Offset:  0,
			Length:  0,
			BuildId: ignoreID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, simpleBase))

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsBaseBeforeDiffNoOverlap(t *testing.T) {
	t.Parallel()
	diff := []BuildMap{
		{
			Offset:  7 * blockSize,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, []BuildMap{
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

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsDiffBeforeBaseNoOverlap(t *testing.T) {
	t.Parallel()
	diff := []BuildMap{
		{
			Offset:  0,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, []BuildMap{
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

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsBaseInsideDiff(t *testing.T) {
	t.Parallel()
	diff := []BuildMap{
		{
			Offset:  1 * blockSize,
			Length:  5 * blockSize,
			BuildId: diffID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, []BuildMap{
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

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsDiffInsideBase(t *testing.T) {
	t.Parallel()
	diff := []BuildMap{
		{
			Offset:  3 * blockSize,
			Length:  1 * blockSize,
			BuildId: diffID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, []BuildMap{
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

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsBaseAfterDiffWithOverlap(t *testing.T) {
	t.Parallel()
	diff := []BuildMap{
		{
			Offset:  1 * blockSize,
			Length:  4 * blockSize,
			BuildId: diffID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, []BuildMap{
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

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestMergeMappingsDiffAfterBaseWithOverlap(t *testing.T) {
	t.Parallel()
	diff := []BuildMap{
		{
			Offset:  3 * blockSize,
			Length:  4 * blockSize,
			BuildId: diffID,
		},
	}

	m, err := MergeMappings(simpleBase, diff)
	require.NoError(t, err)

	require.True(t, Equal(m, []BuildMap{
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

	err = ValidateMappings(m, size, blockSize)

	require.NoError(t, err)
}

func TestNormalizeMappingsEmptySlice(t *testing.T) {
	t.Parallel()
	m := NormalizeMappings([]BuildMap{})
	assert.Empty(t, m)
}

func TestNormalizeMappingsSingleMapping(t *testing.T) {
	t.Parallel()
	input := []BuildMap{
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

	input := []BuildMap{
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
	input := []BuildMap{
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
	input := []BuildMap{
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

	input := []BuildMap{
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

	input := []BuildMap{
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
	input := []BuildMap{
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

	input := []BuildMap{
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
	input := []BuildMap{
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
	input := []BuildMap{
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

// makeFrameTable builds a FrameTable with n frames of uniform uncompressed size
// frameU, compressed size frameC, starting at offset 0.
func makeFrameTable(n int, frameU, frameC int32) *storage.FrameTable {
	ft := storage.NewFrameTable(storage.CompressionZstd)
	ft.Offset = storage.FrameOffset{U: 0, C: 0}
	for range n {
		ft.Frames = append(ft.Frames, storage.FrameSize{U: frameU, C: frameC})
	}

	return ft
}

func assertFrameTable(t *testing.T, label string, m *BuildMap, startU, startC int64, nFrames int, frameU, frameC int32) {
	t.Helper()

	require.NotNil(t, m.FrameTable, "%s: FrameTable should not be nil", label)
	assert.Equal(t, startU, m.FrameTable.Offset.U, "%s: Offset.U", label)
	assert.Equal(t, startC, m.FrameTable.Offset.C, "%s: Offset.C", label)
	require.Len(t, m.FrameTable.Frames, nFrames, "%s: frame count", label)
	for i, f := range m.FrameTable.Frames {
		assert.Equal(t, frameU, f.U, "%s: frame[%d].U", label, i)
		assert.Equal(t, frameC, f.C, "%s: frame[%d].C", label, i)
	}
}

func TestMergeMappings_FrameTableSplits(t *testing.T) {
	t.Parallel()

	// Frame geometry: each frame = 1 block uncompressed, compressed to 1 MiB.
	frameU := int32(blockSize)
	frameC := int32(1 << 20)

	compBaseID := uuid.New()
	compDiffID := uuid.New()
	plainID := uuid.New()

	// 6-frame base FrameTable covering blocks [0..6).
	//   frame i: U=[i*2M, (i+1)*2M)  C=[i*1M, (i+1)*1M)
	baseFT := makeFrameTable(6, frameU, frameC)

	tests := map[string]struct {
		base     []*BuildMap
		diff     []*BuildMap
		validate func(t *testing.T, merged []*BuildMap)
	}{
		"diff inside base — left and right get correct frame subsets": {
			base: []*BuildMap{{
				Offset: 0, Length: 6 * blockSize,
				BuildId: compBaseID, BuildStorageOffset: 0,
				FrameTable: baseFT,
			}},
			diff: []*BuildMap{{
				Offset: 2 * blockSize, Length: 2 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []*BuildMap) {
				t.Helper()
				require.Len(t, m, 3)

				assert.Equal(t, uint64(0), m[0].Offset)
				assert.Equal(t, 2*blockSize, m[0].Length)
				assertFrameTable(t, "left", m[0], 0, 0, 2, frameU, frameC)

				assert.Equal(t, compDiffID, m[1].BuildId)

				assert.Equal(t, 4*blockSize, m[2].Offset)
				assert.Equal(t, 4*blockSize, m[2].BuildStorageOffset)
				assertFrameTable(t, "right", m[2],
					4*int64(frameU), 4*int64(frameC), 2, frameU, frameC)
			},
		},

		"base after diff with overlap — right split keeps tail frames": {
			base: []*BuildMap{
				{Offset: 0, Length: 1 * blockSize, BuildId: plainID},
				{
					Offset: 1 * blockSize, Length: 4 * blockSize,
					BuildId: compBaseID, BuildStorageOffset: 0,
					FrameTable: makeFrameTable(4, frameU, frameC),
				},
			},
			diff: []*BuildMap{{
				Offset: 0, Length: 3 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []*BuildMap) {
				t.Helper()
				require.Len(t, m, 2)

				assert.Equal(t, compDiffID, m[0].BuildId)

				assert.Equal(t, 3*blockSize, m[1].Offset)
				assert.Equal(t, 2*blockSize, m[1].BuildStorageOffset)
				assertFrameTable(t, "right-tail", m[1],
					2*int64(frameU), 2*int64(frameC), 2, frameU, frameC)
			},
		},

		"diff after base with overlap — left split keeps head frames": {
			base: []*BuildMap{
				{
					Offset: 0, Length: 4 * blockSize,
					BuildId: compBaseID, BuildStorageOffset: 0,
					FrameTable: makeFrameTable(4, frameU, frameC),
				},
				{Offset: 4 * blockSize, Length: 2 * blockSize, BuildId: plainID},
			},
			diff: []*BuildMap{{
				Offset: 2 * blockSize, Length: 4 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []*BuildMap) {
				t.Helper()
				require.Len(t, m, 2)

				assert.Equal(t, uint64(0), m[0].Offset)
				assert.Equal(t, 2*blockSize, m[0].Length)
				assertFrameTable(t, "left-head", m[0], 0, 0, 2, frameU, frameC)

				assert.Equal(t, compDiffID, m[1].BuildId)
			},
		},

		"two diffs split same base into three pieces": {
			base: []*BuildMap{{
				Offset: 0, Length: 6 * blockSize,
				BuildId: compBaseID, BuildStorageOffset: 0,
				FrameTable: baseFT,
			}},
			diff: []*BuildMap{
				{Offset: 1 * blockSize, Length: 1 * blockSize, BuildId: compDiffID},
				{Offset: 4 * blockSize, Length: 1 * blockSize, BuildId: compDiffID},
			},
			validate: func(t *testing.T, m []*BuildMap) {
				t.Helper()
				require.Len(t, m, 5)

				assertFrameTable(t, "piece-0", m[0], 0, 0, 1, frameU, frameC)
				assert.Equal(t, compDiffID, m[1].BuildId)
				assertFrameTable(t, "piece-2", m[2],
					2*int64(frameU), 2*int64(frameC), 2, frameU, frameC)
				assert.Equal(t, compDiffID, m[3].BuildId)
				assertFrameTable(t, "piece-4", m[4],
					5*int64(frameU), 5*int64(frameC), 1, frameU, frameC)
			},
		},

		"nil FrameTable base — splits work without frames": {
			base: []*BuildMap{{
				Offset: 0, Length: 4 * blockSize,
				BuildId: compBaseID, BuildStorageOffset: 0,
			}},
			diff: []*BuildMap{{
				Offset: 1 * blockSize, Length: 2 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []*BuildMap) {
				t.Helper()
				require.Len(t, m, 3)
				assert.Nil(t, m[0].FrameTable)
				assert.Nil(t, m[2].FrameTable)
			},
		},

		"multi-layer base with three compressed builds — diff splits middle build": {
			// Simulates a real multi-layer header: three builds (A, B, C) each
			// with their own FrameTable. A diff lands inside build B, splitting
			// it. Builds A and C must pass through with FrameTables intact.
			base: func() []*BuildMap {
				buildA := uuid.New()
				buildB := compBaseID
				buildC := uuid.New()

				return []*BuildMap{
					{
						Offset: 0, Length: 2 * blockSize,
						BuildId: buildA, BuildStorageOffset: 0,
						FrameTable: makeFrameTable(2, frameU, frameC),
					},
					{
						Offset: 2 * blockSize, Length: 4 * blockSize,
						BuildId: buildB, BuildStorageOffset: 0,
						FrameTable: makeFrameTable(4, frameU, frameC),
					},
					{
						Offset: 6 * blockSize, Length: 2 * blockSize,
						BuildId: buildC, BuildStorageOffset: 0,
						FrameTable: makeFrameTable(2, frameU, frameC),
					},
				}
			}(),
			diff: []*BuildMap{{
				Offset: 3 * blockSize, Length: 2 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []*BuildMap) {
				t.Helper()
				// Expected: A(untouched) | B-left[0..1) | diff | B-right[3..4) | C(untouched)
				require.Len(t, m, 5)

				// Build A: untouched, full 2-frame FT.
				assertFrameTable(t, "build-A", m[0], 0, 0, 2, frameU, frameC)

				// Build B left: block [2*bs..3*bs), storage offset 0, frame 0 only.
				assert.Equal(t, 2*blockSize, m[1].Offset)
				assert.Equal(t, 1*blockSize, m[1].Length)
				assert.Equal(t, uint64(0), m[1].BuildStorageOffset)
				assertFrameTable(t, "build-B-left", m[1], 0, 0, 1, frameU, frameC)

				// Diff
				assert.Equal(t, compDiffID, m[2].BuildId)

				// Build B right: block [5*bs..6*bs), storage offset 3*bs, frame 3.
				assert.Equal(t, 5*blockSize, m[3].Offset)
				assert.Equal(t, 1*blockSize, m[3].Length)
				assert.Equal(t, 3*blockSize, m[3].BuildStorageOffset)
				assertFrameTable(t, "build-B-right", m[3],
					3*int64(frameU), 3*int64(frameC), 1, frameU, frameC)

				// Build C: untouched, full 2-frame FT.
				assert.Equal(t, 6*blockSize, m[4].Offset)
				assertFrameTable(t, "build-C", m[4], 0, 0, 2, frameU, frameC)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			merged, err := MergeMappings(tc.base, tc.diff)
			require.NoError(t, err)

			tc.validate(t, merged)

			// Verify the invariant that the read path depends on:
			// for every mapping with a FrameTable, LocateCompressed must be
			// able to find frames at both the start and end of the mapping's
			// storage range. This is what GetShiftedMapping + Chunker.fetch
			// rely on.
			for i, m := range merged {
				ft := m.FrameTable
				if ft == nil {
					continue
				}

				label := fmt.Sprintf("mapping[%d] offset=%d storage=%d len=%d",
					i, m.Offset, m.BuildStorageOffset, m.Length)

				// Offset must be at or before the mapping's storage offset.
				require.LessOrEqual(t, ft.Offset.U, int64(m.BuildStorageOffset),
					"%s: FrameTable.Offset.U must be <= BuildStorageOffset", label)

				// LocateCompressed must find the first block.
				_, err := ft.LocateCompressed(int64(m.BuildStorageOffset))
				require.NoError(t, err, "%s: LocateCompressed(start)", label)

				// LocateCompressed must find the last block.
				if m.Length > 0 {
					lastByte := int64(m.BuildStorageOffset + m.Length - 1)
					_, err := ft.LocateCompressed(lastByte)
					require.NoError(t, err, "%s: LocateCompressed(last byte)", label)
				}
			}
		})
	}
}
