package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestUploadMetricFileType(t *testing.T) {
	t.Parallel()

	require.Equal(t, "memfile", uploadMetricFileType(storage.MemfileName))
	require.Equal(t, "rootfs", uploadMetricFileType(storage.RootfsName))
}
