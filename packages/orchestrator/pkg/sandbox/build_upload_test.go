//go:build linux

package sandbox

import (
	"testing"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blockmocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/mocks"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	templatemocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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

// putV3Header registers a V3 ancestor in the fake cache. V3 headers carry no
// Builds map at all — appendAncestorBuilds must synthesize a placeholder from
// Metadata.Size so descendants don't pay a refresh roundtrip per cold read.
func putV3Header(t *testing.T, cache *fakeCache, buildID uuid.UUID, fileType build.DiffType, size uint64) {
	t.Helper()
	tpl := templatemocks.NewMockTemplate(t)
	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(&headers.Header{
		Metadata: &headers.Metadata{Version: 3, Size: size},
	}).Maybe()

	switch fileType {
	case build.Memfile:
		tpl.EXPECT().Memfile(mock.Anything).Return(dev, nil).Maybe()
	case build.Rootfs:
		tpl.EXPECT().Rootfs().Return(dev, nil).Maybe()
	}

	cache.put(buildID.String(), tpl)
}

func mappingTo(t *testing.T, blockSize uint64, ancestorID uuid.UUID, length uint64) headers.Mapping {
	t.Helper()
	m, err := headers.NewMapping(blockSize, []headers.BuildMap{{
		Offset: 0, Length: length, BuildId: ancestorID, BuildStorageOffset: 0,
	}})
	require.NoError(t, err)

	return m
}

// V4 descendant of a V3 ancestor: appendAncestorBuilds writes a sentinel
// empty BuildData{} so descendants take createDiff's hasEntry branch and
// resolve() short-circuits on the UncompressedFrameTable hint from
// GetBuildFrameData. Size is left zero on purpose — Metadata.Size is the
// virtual size, not the diff size, and asking storage at upload time across
// a long chain would multiply roundtrips. createDiff already falls back to
// upstream.Size when bd.Size == 0.
func TestAppendAncestorBuilds_V3AncestorSynthesizesEntry(t *testing.T) {
	t.Parallel()

	uploads, cache := newUploads(t)
	ancestorID := uuid.New()
	putV3Header(t, cache, ancestorID, build.Memfile, 512*1024*1024)

	u := &Upload{buildID: uuid.New(), uploads: uploads}
	dst := map[uuid.UUID]headers.BuildData{}

	err := u.appendAncestorBuilds(t.Context(), dst, mappingTo(t, 4096, ancestorID, 4096), build.Memfile)
	require.NoError(t, err)

	bd, ok := dst[ancestorID]
	require.True(t, ok, "V3 ancestor must produce a Builds entry")
	require.Equal(t, int64(0), bd.Size, "Size must stay 0 — diff size is queried at read time via upstream.Size")
	require.Nil(t, bd.FrameData, "FrameData must be nil so GetBuildFrameData returns UncompressedFrameTable")
}

// V4 ancestor: existing behavior — copy the ancestor's own self-entry verbatim.
func TestAppendAncestorBuilds_V4AncestorCopiesEntry(t *testing.T) {
	t.Parallel()

	uploads, cache := newUploads(t)
	ancestorID := uuid.New()
	putHeader(t, cache, ancestorID, build.Memfile, false)

	u := &Upload{buildID: uuid.New(), uploads: uploads}
	dst := map[uuid.UUID]headers.BuildData{}

	err := u.appendAncestorBuilds(t.Context(), dst, mappingTo(t, 4096, ancestorID, 4096), build.Memfile)
	require.NoError(t, err)

	_, ok := dst[ancestorID]
	require.True(t, ok, "V4 ancestor's self-entry must be copied into dst")
}

// V3 caller (dst=nil): the barrier still runs, but no entry is written —
// preserves the existing contract.
func TestAppendAncestorBuilds_NilDstSkipsSynthesis(t *testing.T) {
	t.Parallel()

	uploads, cache := newUploads(t)
	ancestorID := uuid.New()
	putV3Header(t, cache, ancestorID, build.Memfile, 1024)

	u := &Upload{buildID: uuid.New(), uploads: uploads}
	err := u.appendAncestorBuilds(t.Context(), nil, mappingTo(t, 4096, ancestorID, 4096), build.Memfile)
	require.NoError(t, err)
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
