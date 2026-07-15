package ensurefreedisk

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
)

func TestComputeGrownSize(t *testing.T) {
	t.Parallel()

	const currentSize = int64(1 << 30)
	oneMiB := units.MBToBytes(1)
	size, err := computeGrownSize(currentSize, units.MBToBytes(512), units.MBToBytes(512)-1)
	require.NoError(t, err)
	require.Equal(t, currentSize+oneMiB, size)

	size, err = computeGrownSize(currentSize, units.MBToBytes(512), -oneMiB)
	require.NoError(t, err)
	require.Equal(t, currentSize+units.MBToBytes(513), size)

	_, err = computeGrownSize(math.MaxInt64, 1, 0)
	require.Error(t, err)
}

func TestValidateSourceGeometry(t *testing.T) {
	t.Parallel()

	const size = int64(16 << 20)
	require.NoError(t, validateSourceGeometry(size, header.RootfsBlockSize, uint64(size), header.RootfsBlockSize))
	require.Error(t, validateSourceGeometry(size+header.RootfsBlockSize, header.RootfsBlockSize, uint64(size), header.RootfsBlockSize))
	require.Error(t, validateSourceGeometry(size, 2*header.RootfsBlockSize, uint64(size), 2*header.RootfsBlockSize))
}
