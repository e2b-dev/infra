package fcversion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ParsesWithCommit(t *testing.T) {
	t.Parallel()

	info, err := New("v1.12.1_210cbac")
	require.NoError(t, err)

	ver := info.Version()
	assert.Equal(t, uint64(1), ver.Major())
	assert.Equal(t, uint64(12), ver.Minor())
	assert.Equal(t, uint64(1), ver.Patch())
}

func TestNew_ParsesWithoutCommit(t *testing.T) {
	t.Parallel()

	info, err := New("v1.10.1")
	require.NoError(t, err)

	ver := info.Version()
	assert.Equal(t, uint64(1), ver.Major())
	assert.Equal(t, uint64(10), ver.Minor())
}

func TestNew_ParsesWithoutVPrefix(t *testing.T) {
	t.Parallel()

	info, err := New("1.12.0_deadbee")
	require.NoError(t, err)

	assert.Equal(t, uint64(12), info.Version().Minor())
}

func TestNew_RejectsGarbage(t *testing.T) {
	t.Parallel()

	_, err := New("not-a-version")
	assert.Error(t, err)
}

// TestHasHugePages pins the huge-pages support boundary: firecracker v1.7.
// The build path relies on this: HugePages determines whether the memfile
// uses 2 MiB or 4 KiB pages, and a wrong answer corrupts the memory file
// for the binary that is actually launched. Do not relax these assertions
// without also making sure the orchestrator's Builder.Build still computes
// HugePages from the firecracker version it resolved locally.
func TestHasHugePages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		version string
		want    bool
	}{
		// Below the 1.7 boundary: no huge-pages.
		{"v1.5.0_abc1234", false},
		{"v1.6.9_abc1234", false},
		// At and above 1.7: huge-pages.
		{"v1.7.0_abc1234", true},
		{"v1.10.1_30cbb07", true},
		{"v1.12.1_210cbac", true},
		{"v1.14.1_458ca91", true},
		// Future major versions must stay on the huge-pages side.
		{"v2.0.0_deadbee", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()

			info, err := New(tc.version)
			require.NoError(t, err)

			assert.Equal(t, tc.want, info.HasHugePages())
		})
	}
}
