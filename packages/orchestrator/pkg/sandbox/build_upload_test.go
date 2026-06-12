//go:build linux

package sandbox

import (
	"testing"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func newV4HeaderFF(t *testing.T, on bool) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.V4HeaderForUncompressedFlag.Key()).VariationForAll(on))

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = ff.Close(t.Context())
	})

	return ff
}

func resolveV4(t *testing.T, ff *featureflags.Client) bool {
	t.Helper()
	_, useV4, err := resolveCompressConfig(t.Context(), storage.CompressConfig{}, ff, storage.MemfileName, 4096, storage.UseCaseBuild)
	require.NoError(t, err)

	return useV4
}

func TestResolveCompressConfig_V4_NilClient(t *testing.T) {
	t.Parallel()

	require.False(t, resolveV4(t, nil))
}

func TestResolveCompressConfig_V4_FlagOff(t *testing.T) {
	t.Parallel()

	ff := newV4HeaderFF(t, false)
	require.False(t, resolveV4(t, ff))
}

func TestResolveCompressConfig_V4_FlagOn(t *testing.T) {
	t.Parallel()

	ff := newV4HeaderFF(t, true)
	require.True(t, resolveV4(t, ff))
}

// A filesystem-only snapshot has no memfile, so its MemorySnapshot.BlockSize is
// 0. NewUpload must skip resolving the memfile compress config for it —
// otherwise, with compression enabled, validateCompressConfig would reject the
// zero block size and fail the upload. FrameSizeKB is a multiple of the 4 KiB
// rootfs block so the rootfs config (which is always resolved) stays valid.
func TestNewUpload_FilesystemSnapshotSkipsMemfileCompressConfig(t *testing.T) {
	t.Parallel()

	cfg := storage.CompressConfig{Enabled: true, Type: "zstd", FrameSizeKB: 256}

	t.Run("filesystem-only snapshot with zero memfile block size succeeds", func(t *testing.T) {
		t.Parallel()
		snap := &Snapshot{
			BuildID:            uuid.New(),
			FilesystemSnapshot: true,
			RootfsBlockSize:    4096,
		}

		u, err := NewUpload(t.Context(), nil, snap, nil, cfg, nil, storage.UseCaseBuild, storage.ObjectMetadata{})
		require.NoError(t, err)
		require.NotNil(t, u)
	})

	t.Run("memory snapshot with zero memfile block size still errors", func(t *testing.T) {
		t.Parallel()
		snap := &Snapshot{
			BuildID:            uuid.New(),
			FilesystemSnapshot: false,
			RootfsBlockSize:    4096,
		}

		_, err := NewUpload(t.Context(), nil, snap, nil, cfg, nil, storage.UseCaseBuild, storage.ObjectMetadata{})
		require.Error(t, err)
	})
}
