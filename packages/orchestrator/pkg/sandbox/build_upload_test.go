//go:build linux

package sandbox

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
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

func TestUploadRunV3MemorylessSkipsMemoryArtifacts(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	metaPath := t.TempDir() + "/metadata.json"
	require.NoError(t, os.WriteFile(metaPath, []byte("{}"), 0o644))

	store := storage.NewMockStorageProvider(t)
	metadataBlob := storage.NewMockBlob(t)
	store.EXPECT().
		OpenBlob(mock.Anything, mock.MatchedBy(func(path string) bool {
			return path == (storage.Paths{BuildID: buildID.String()}).Metadata()
		}), storage.MetadataObjectType).
		Return(metadataBlob, nil)
	metadataBlob.EXPECT().Put(mock.Anything, []byte("{}"), mock.Anything).Return(nil)

	upload := &Upload{
		buildID: buildID,
		snap: &Snapshot{
			BuildID:           buildID,
			MemorySnapshot:    false,
			MemfileDiff:       &build.NoDiff{},
			MemfileDiffHeader: NewResolvedDiffHeader(nil),
			RootfsDiff:        &build.NoDiff{},
			RootfsDiffHeader:  NewResolvedDiffHeader(nil),
			Metafile:          sbxtemplate.NewLocalFileLink(metaPath),
		},
		paths: storage.Paths{BuildID: buildID.String()},
		store: store,
	}

	require.NoError(t, upload.runV3(t.Context()))
}
