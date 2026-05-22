package header

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	m := MergeMappings(simpleBase, diff)

	require.True(t, Equal(m, simpleBase))

	err := ValidateMappings(m, size, blockSize)

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

	m := MergeMappings(simpleBase, diff)

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

	err := ValidateMappings(m, size, blockSize)

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

	m := MergeMappings(simpleBase, diff)

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

	err := ValidateMappings(m, size, blockSize)

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

	m := MergeMappings(simpleBase, diff)

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

	err := ValidateMappings(m, size, blockSize)

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

	m := MergeMappings(simpleBase, diff)

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

	err := ValidateMappings(m, size, blockSize)

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

	m := MergeMappings(simpleBase, diff)

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

	err := ValidateMappings(m, size, blockSize)

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

	m := MergeMappings(simpleBase, diff)

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

	err := ValidateMappings(m, size, blockSize)

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

// TestMergeMappings_Splits verifies that MergeMappings preserves
// BuildStorageOffset through splits. When a diff lands in the middle of a
// base mapping the base is split into left/right pieces; each piece must
// keep the correct BuildStorageOffset so the read path fetches data from
// the right position within each build's data blob. Without this,
// compressed builds whose frame tables are keyed by BuildStorageOffset
// would decompress the wrong frames.
func TestMergeMappings_Splits(t *testing.T) {
	t.Parallel()

	compBaseID := uuid.New()
	compDiffID := uuid.New()
	plainID := uuid.New()

	tests := map[string]struct {
		base     []BuildMap
		diff     []BuildMap
		validate func(t *testing.T, merged []BuildMap)
	}{
		"diff inside base — left and right split correctly": {
			base: []BuildMap{{
				Offset: 0, Length: 6 * blockSize,
				BuildId: compBaseID, BuildStorageOffset: 0,
			}},
			diff: []BuildMap{{
				Offset: 2 * blockSize, Length: 2 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []BuildMap) {
				t.Helper()
				require.Len(t, m, 3)

				assert.Equal(t, uint64(0), m[0].Offset)
				assert.Equal(t, 2*blockSize, m[0].Length)
				assert.Equal(t, compBaseID, m[0].BuildId)

				assert.Equal(t, compDiffID, m[1].BuildId)

				assert.Equal(t, 4*blockSize, m[2].Offset)
				assert.Equal(t, 4*blockSize, m[2].BuildStorageOffset)
				assert.Equal(t, compBaseID, m[2].BuildId)
			},
		},

		"base after diff with overlap — right split keeps tail": {
			base: []BuildMap{
				{Offset: 0, Length: 1 * blockSize, BuildId: plainID},
				{
					Offset: 1 * blockSize, Length: 4 * blockSize,
					BuildId: compBaseID, BuildStorageOffset: 0,
				},
			},
			diff: []BuildMap{{
				Offset: 0, Length: 3 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []BuildMap) {
				t.Helper()
				require.Len(t, m, 2)

				assert.Equal(t, compDiffID, m[0].BuildId)

				assert.Equal(t, 3*blockSize, m[1].Offset)
				assert.Equal(t, 2*blockSize, m[1].BuildStorageOffset)
				assert.Equal(t, compBaseID, m[1].BuildId)
			},
		},

		"diff after base with overlap — left split keeps head": {
			base: []BuildMap{
				{
					Offset: 0, Length: 4 * blockSize,
					BuildId: compBaseID, BuildStorageOffset: 0,
				},
				{Offset: 4 * blockSize, Length: 2 * blockSize, BuildId: plainID},
			},
			diff: []BuildMap{{
				Offset: 2 * blockSize, Length: 4 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []BuildMap) {
				t.Helper()
				require.Len(t, m, 2)

				assert.Equal(t, uint64(0), m[0].Offset)
				assert.Equal(t, 2*blockSize, m[0].Length)
				assert.Equal(t, compBaseID, m[0].BuildId)

				assert.Equal(t, compDiffID, m[1].BuildId)
			},
		},

		"two diffs split same base into three pieces": {
			base: []BuildMap{{
				Offset: 0, Length: 6 * blockSize,
				BuildId: compBaseID, BuildStorageOffset: 0,
			}},
			diff: []BuildMap{
				{Offset: 1 * blockSize, Length: 1 * blockSize, BuildId: compDiffID},
				{Offset: 4 * blockSize, Length: 1 * blockSize, BuildId: compDiffID},
			},
			validate: func(t *testing.T, m []BuildMap) {
				t.Helper()
				require.Len(t, m, 5)

				assert.Equal(t, compBaseID, m[0].BuildId)
				assert.Equal(t, 1*blockSize, m[0].Length)

				assert.Equal(t, compDiffID, m[1].BuildId)

				assert.Equal(t, compBaseID, m[2].BuildId)
				assert.Equal(t, 2*blockSize, m[2].Length)
				assert.Equal(t, 2*blockSize, m[2].BuildStorageOffset)

				assert.Equal(t, compDiffID, m[3].BuildId)

				assert.Equal(t, compBaseID, m[4].BuildId)
				assert.Equal(t, 1*blockSize, m[4].Length)
				assert.Equal(t, 5*blockSize, m[4].BuildStorageOffset)
			},
		},

		"multi-layer base — diff splits middle build": {
			base: func() []BuildMap {
				buildA := uuid.New()
				buildB := compBaseID
				buildC := uuid.New()

				return []BuildMap{
					{
						Offset: 0, Length: 2 * blockSize,
						BuildId: buildA, BuildStorageOffset: 0,
					},
					{
						Offset: 2 * blockSize, Length: 4 * blockSize,
						BuildId: buildB, BuildStorageOffset: 0,
					},
					{
						Offset: 6 * blockSize, Length: 2 * blockSize,
						BuildId: buildC, BuildStorageOffset: 0,
					},
				}
			}(),
			diff: []BuildMap{{
				Offset: 3 * blockSize, Length: 2 * blockSize,
				BuildId: compDiffID,
			}},
			validate: func(t *testing.T, m []BuildMap) {
				t.Helper()
				require.Len(t, m, 5)

				// Build B left
				assert.Equal(t, 2*blockSize, m[1].Offset)
				assert.Equal(t, 1*blockSize, m[1].Length)
				assert.Equal(t, uint64(0), m[1].BuildStorageOffset)

				// Diff
				assert.Equal(t, compDiffID, m[2].BuildId)

				// Build B right
				assert.Equal(t, 5*blockSize, m[3].Offset)
				assert.Equal(t, 1*blockSize, m[3].Length)
				assert.Equal(t, 3*blockSize, m[3].BuildStorageOffset)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			merged := MergeMappings(tc.base, tc.diff)

			tc.validate(t, merged)
		})
	}
}
