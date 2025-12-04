package ex

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestReinsertCandidates(t *testing.T) {
	c := NewCleaner(Options{Path: t.TempDir(), DeleteN: 1, BatchN: 1, TargetBytesToDelete: 1}, logger.NewNopLogger())

	// parent := &Dir{Name: "parent"}

	// Add additional non-empty dirs to exercise insertion into first, middle, and last positions.
	p1 := &Dir{Name: "first"}
	p1.Files = []File{
		{Name: "old_a.txt", ATimeUnix: 100, Size: 1},
		{Name: "old_b.txt", ATimeUnix: 50, Size: 1},
	}
	p1.Sort()

	p2 := &Dir{Name: "middle"}
	p2.Files = []File{
		{Name: "m_old1.txt", ATimeUnix: 300, Size: 1},
		{Name: "m_old2.txt", ATimeUnix: 100, Size: 1},
	}
	p2.Sort()

	p3 := &Dir{Name: "last"}
	p3.Files = []File{
		{Name: "l_old1.txt", ATimeUnix: 300, Size: 1},
		{Name: "l_old2.txt", ATimeUnix: 200, Size: 1},
	}
	p3.Sort()

	// Candidates that should end up first, middle, and last respectively once reinserted.
	candidatesInitial := []*Candidate{
		{Parent: p1, FullPath: "/first/new_oldest.txt", ATimeUnix: 200, Size: 1},   // oldest in p1
		{Parent: p2, FullPath: "/middle/new_middle1.txt", ATimeUnix: 200, Size: 1}, // middle-aged in p2
		{Parent: p2, FullPath: "/middle/new_middle2.txt", ATimeUnix: 201, Size: 1},
		{Parent: p3, FullPath: "/last/new_youngest.txt", ATimeUnix: 100, Size: 1}, // youngest in p3
		{Parent: p3, FullPath: "/last/new_middle.txt", ATimeUnix: 201, Size: 1},   // middle in p3
	}

	c.reinsertCandidates(candidatesInitial)

	// Verify insertion positions relative to existing files.
	require.Len(t, p1.Files, 3)
	require.Equal(t, "old_b.txt", p1.Files[0].Name)
	require.Equal(t, 50, int(p1.Files[0].ATimeUnix))
	require.Equal(t, "old_a.txt", p1.Files[1].Name)
	require.Equal(t, 100, int(p1.Files[1].ATimeUnix))
	require.Equal(t, "new_oldest.txt", p1.Files[2].Name)
	require.Equal(t, 200, int(p1.Files[2].ATimeUnix))

	require.Len(t, p2.Files, 4)
	require.Equal(t, "m_old2.txt", p2.Files[0].Name)
	require.Equal(t, 100, int(p2.Files[0].ATimeUnix))
	require.Equal(t, "new_middle1.txt", p2.Files[1].Name)
	require.Equal(t, 200, int(p2.Files[1].ATimeUnix))
	require.Equal(t, "new_middle2.txt", p2.Files[2].Name)
	require.Equal(t, 201, int(p2.Files[2].ATimeUnix))
	require.Equal(t, "m_old1.txt", p2.Files[3].Name)
	require.Equal(t, 300, int(p2.Files[3].ATimeUnix))

	require.Len(t, p3.Files, 4)
	require.Equal(t, "new_youngest.txt", p3.Files[0].Name)
	require.Equal(t, 100, int(p3.Files[0].ATimeUnix))
	require.Equal(t, "l_old2.txt", p3.Files[1].Name)
	require.Equal(t, 200, int(p3.Files[1].ATimeUnix))
	require.Equal(t, "new_middle.txt", p3.Files[2].Name)
	require.Equal(t, 201, int(p3.Files[2].ATimeUnix))
	require.Equal(t, "l_old1.txt", p3.Files[3].Name)
	require.Equal(t, 300, int(p3.Files[3].ATimeUnix))
}
