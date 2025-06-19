package header

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
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
