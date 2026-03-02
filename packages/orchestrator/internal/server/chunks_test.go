package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blockmocks "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/mocks"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	tmpl "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	templatemocks "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratormocks "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	storageheader "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// --- stub types ---

// memDiff is an in-memory DiffSource for tests.
type memDiff struct {
	data      []byte
	blockSize int64
}

func (m *memDiff) Slice(_ context.Context, off, length int64) ([]byte, error) {
	if off >= int64(len(m.data)) {
		return nil, nil
	}

	end := min(off+length, int64(len(m.data)))

	return m.data[off:end], nil
}

func (m *memDiff) Size(_ context.Context) (int64, error) {
	return int64(len(m.data)), nil
}

func (m *memDiff) BlockSize() int64 {
	if m.blockSize > 0 {
		return m.blockSize
	}

	return int64(len(m.data))
}

// stubChunkCache is a manual stub for the unexported chunkCache interface.
type stubChunkCache struct {
	diff          tmpl.DiffSource
	diffFound     bool
	template      tmpl.Template
	templateFound bool
}

func (s *stubChunkCache) LookupDiff(_ string, _ build.DiffType) (tmpl.DiffSource, bool) {
	return s.diff, s.diffFound
}

func (s *stubChunkCache) GetCachedTemplate(_ string) (tmpl.Template, bool) {
	return s.template, s.templateFound
}

// --- helpers ---

func newStream(t *testing.T) (*orchestratormocks.MockChunkService_GetBuildFileServer, *[]*orchestrator.GetBuildFileResponse) {
	t.Helper()

	var sent []*orchestrator.GetBuildFileResponse
	stream := orchestratormocks.NewMockChunkService_GetBuildFileServer(t)
	stream.EXPECT().Send(mock.Anything).RunAndReturn(func(r *orchestrator.GetBuildFileResponse) error {
		sent = append(sent, r)
		return nil
	}).Maybe()
	stream.EXPECT().Context().Return(context.Background()).Maybe()

	return stream, &sent
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func collectStreamData(msgs []*orchestrator.GetBuildFileResponse) []byte {
	var out []byte
	for _, m := range msgs {
		out = append(out, m.GetData()...)
	}

	return out
}

// --- diffTypeFromFileName ---

func TestDiffTypeFromFileName(t *testing.T) {
	t.Parallel()

	dt, ok := diffTypeFromFileName(storage.MemfileName)
	assert.True(t, ok)
	assert.Equal(t, build.Memfile, dt)

	dt, ok = diffTypeFromFileName(storage.RootfsName)
	assert.True(t, ok)
	assert.Equal(t, build.Rootfs, dt)

	_, ok = diffTypeFromFileName(storage.SnapfileName)
	assert.False(t, ok)

	_, ok = diffTypeFromFileName(storage.MetadataName)
	assert.False(t, ok)

	_, ok = diffTypeFromFileName(storage.MemfileName + storage.HeaderSuffix)
	assert.False(t, ok)

	_, ok = diffTypeFromFileName(storage.RootfsName + storage.HeaderSuffix)
	assert.False(t, ok)
}

// --- GetBuildFileInfo ---

func TestGetBuildFileInfo_DiffFile_Available(t *testing.T) {
	t.Parallel()

	cache := &stubChunkCache{
		diff:      &memDiff{data: make([]byte, 1234)},
		diffFound: true,
	}
	resp, err := getBuildFileInfo(t.Context(), cache, &orchestrator.GetBuildFileInfoRequest{
		BuildId:  "build-1",
		FileName: storage.MemfileName,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1234), resp.GetTotalSize())
	assert.False(t, resp.GetNotAvailable())
}

func TestGetBuildFileInfo_DiffFile_NotAvailable(t *testing.T) {
	t.Parallel()

	cache := &stubChunkCache{diffFound: false}
	resp, err := getBuildFileInfo(t.Context(), cache, &orchestrator.GetBuildFileInfoRequest{
		BuildId:  "build-1",
		FileName: storage.RootfsName,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetNotAvailable())
}

func TestGetBuildFileInfo_RejectsNonDiffFile(t *testing.T) {
	t.Parallel()

	cache := &stubChunkCache{}
	for _, name := range []string{
		storage.SnapfileName,
		storage.MetadataName,
		storage.MemfileName + storage.HeaderSuffix,
		storage.RootfsName + storage.HeaderSuffix,
	} {
		_, err := getBuildFileInfo(t.Context(), cache, &orchestrator.GetBuildFileInfoRequest{
			BuildId:  "build-1",
			FileName: name,
		})
		assert.Error(t, err, "expected error for %q", name)
	}
}

// --- GetBuildFile ---

func TestGetBuildFile_DiffFile_Available(t *testing.T) {
	t.Parallel()

	data := []byte("diff bytes")
	cache := &stubChunkCache{
		diff:      &memDiff{data: data},
		diffFound: true,
	}
	stream, sent := newStream(t)

	err := getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.MemfileName,
		Offset:   0,
		Length:   int64(len(data)),
	}, stream)
	require.NoError(t, err)
	assert.Equal(t, data, collectStreamData(*sent))
}

func TestGetBuildFile_DiffFile_NotAvailable(t *testing.T) {
	t.Parallel()

	cache := &stubChunkCache{diffFound: false}
	stream, sent := newStream(t)

	err := getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.RootfsName,
	}, stream)
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	assert.True(t, (*sent)[0].GetNotAvailable())
}

func TestGetBuildFile_BuildNotInCache(t *testing.T) {
	t.Parallel()

	cache := &stubChunkCache{templateFound: false}
	stream, sent := newStream(t)

	err := getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.SnapfileName,
	}, stream)
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	assert.True(t, (*sent)[0].GetNotAvailable())
}

func TestGetBuildFile_Snapfile(t *testing.T) {
	t.Parallel()

	content := "snapfile content"
	path := writeTempFile(t, content)

	snapfile := templatemocks.NewMockFile(t)
	snapfile.EXPECT().Path().Return(path)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Snapfile().Return(snapfile, nil)

	cache := &stubChunkCache{
		templateFound: true,
		template:      tmplMock,
	}
	stream, sent := newStream(t)

	err := getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.SnapfileName,
	}, stream)
	require.NoError(t, err)
	assert.Equal(t, content, string(collectStreamData(*sent)))
}

func TestGetBuildFile_Metafile(t *testing.T) {
	t.Parallel()

	content := `{"version":2}`
	path := writeTempFile(t, content)

	metafile := templatemocks.NewMockFile(t)
	metafile.EXPECT().Path().Return(path)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().MetadataFile().Return(metafile, nil)

	cache := &stubChunkCache{
		templateFound: true,
		template:      tmplMock,
	}
	stream, sent := newStream(t)

	err := getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.MetadataName,
	}, stream)
	require.NoError(t, err)
	assert.Equal(t, content, string(collectStreamData(*sent)))
}

func TestGetBuildFile_MemfileHeader(t *testing.T) {
	t.Parallel()

	h, err := storageheader.NewHeader(&storageheader.Metadata{Version: 3, BlockSize: 4096, Size: 4096}, nil)
	require.NoError(t, err)

	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(h)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Memfile(mock.Anything).Return(dev, nil)

	cache := &stubChunkCache{
		templateFound: true,
		template:      tmplMock,
	}
	stream, sent := newStream(t)

	err = getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.MemfileName + storage.HeaderSuffix,
	}, stream)
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	assert.NotEmpty(t, (*sent)[0].GetData())
	assert.False(t, (*sent)[0].GetNotAvailable())
}

func TestGetBuildFile_MemfileHeader_Nil(t *testing.T) {
	t.Parallel()

	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(nil)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Memfile(mock.Anything).Return(dev, nil)

	cache := &stubChunkCache{
		templateFound: true,
		template:      tmplMock,
	}
	stream, sent := newStream(t)

	err := getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.MemfileName + storage.HeaderSuffix,
	}, stream)
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	assert.True(t, (*sent)[0].GetNotAvailable())
}

func TestGetBuildFile_RootfsHeader(t *testing.T) {
	t.Parallel()

	h, err := storageheader.NewHeader(&storageheader.Metadata{Version: 3, BlockSize: 4096, Size: 4096}, nil)
	require.NoError(t, err)

	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(h)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Rootfs().Return(dev, nil)

	cache := &stubChunkCache{
		templateFound: true,
		template:      tmplMock,
	}
	stream, sent := newStream(t)

	err = getBuildFile(t.Context(), cache, &orchestrator.GetBuildFileRequest{
		BuildId:  "build-1",
		FileName: storage.RootfsName + storage.HeaderSuffix,
	}, stream)
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	assert.NotEmpty(t, (*sent)[0].GetData())
	assert.False(t, (*sent)[0].GetNotAvailable())
}

// --- streamLocalFile ---

func TestStreamLocalFile_FileExists(t *testing.T) {
	t.Parallel()

	path := writeTempFile(t, "hello world")
	stream, sent := newStream(t)

	err := streamLocalFile(t.Context(), path, stream)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(collectStreamData(*sent)))
}

func TestStreamLocalFile_FileNotExist(t *testing.T) {
	t.Parallel()

	stream, sent := newStream(t)

	err := streamLocalFile(t.Context(), filepath.Join(t.TempDir(), "missing"), stream)
	require.NoError(t, err)
	require.Len(t, *sent, 1)
	assert.True(t, (*sent)[0].GetNotAvailable())
}
