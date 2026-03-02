package template

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratormocks "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
)

// --- peerBlob tests ---

func TestPeerBlob_WriteTo_PeerSucceeds(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildFileClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{Data: []byte("hello ")}, nil).Once()
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{Data: []byte("world")}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "snapfile"
	})).Return(stream, nil)

	blob := &peerBlob{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
	}

	var buf bytes.Buffer
	n, err := blob.WriteTo(context.Background(), &buf)
	require.NoError(t, err)
	assert.Equal(t, int64(11), n)
	assert.Equal(t, "hello world", buf.String())
}

func TestPeerBlob_WriteTo_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildFileClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{NotAvailable: true}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.Anything).Return(stream, nil)

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().WriteTo(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
		n, err := dst.Write([]byte("from gcs"))

		return int64(n), err
	})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile", storage.SnapfileObjectType).Return(baseBlob, nil)

	blob := &peerBlob{
		client:      client,
		base:        base,
		basePath:    "build-1/snapfile",
		baseObjType: storage.SnapfileObjectType,
		buildID:     "build-1",
		fileName:    "snapfile",
	}

	var buf bytes.Buffer
	n, err := blob.WriteTo(context.Background(), &buf)
	require.NoError(t, err)
	assert.Equal(t, int64(8), n)
	assert.Equal(t, "from gcs", buf.String())
}

func TestPeerBlob_WriteTo_PeerError_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.Anything).Return(nil, errors.New("connection refused"))

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().WriteTo(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
		n, err := dst.Write([]byte("from gcs"))

		return int64(n), err
	})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile", storage.SnapfileObjectType).Return(baseBlob, nil)

	blob := &peerBlob{
		client:      client,
		base:        base,
		basePath:    "build-1/snapfile",
		baseObjType: storage.SnapfileObjectType,
		buildID:     "build-1",
		fileName:    "snapfile",
	}

	var buf bytes.Buffer
	_, err := blob.WriteTo(context.Background(), &buf)
	require.NoError(t, err)
	assert.Equal(t, "from gcs", buf.String())
}

func TestPeerBlob_Exists_PeerHasFile(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileInfo(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileInfoRequest) bool {
		return req.GetBuildId() == "build-1"
	})).Return(&orchestrator.GetBuildFileInfoResponse{TotalSize: 100}, nil)

	blob := &peerBlob{client: client, buildID: "build-1", fileName: "snapfile"}
	ok, err := blob.Exists(context.Background())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPeerBlob_Exists_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileInfo(mock.Anything, mock.Anything).Return(&orchestrator.GetBuildFileInfoResponse{NotAvailable: true}, nil)

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().Exists(mock.Anything).Return(true, nil)

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile", storage.SnapfileObjectType).Return(baseBlob, nil)

	blob := &peerBlob{
		client:      client,
		base:        base,
		basePath:    "build-1/snapfile",
		baseObjType: storage.SnapfileObjectType,
		buildID:     "build-1",
		fileName:    "snapfile",
	}

	ok, err := blob.Exists(context.Background())
	require.NoError(t, err)
	assert.True(t, ok)
}

// --- peerSeekable tests ---

func TestPeerSeekable_Size_PeerSucceeds(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileInfo(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileInfoRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == storage.MemfileName
	})).Return(&orchestrator.GetBuildFileInfoResponse{TotalSize: 4096}, nil)

	s := &peerSeekable{client: client, buildID: "build-1", fileName: storage.MemfileName}
	size, err := s.Size(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4096), size)
}

func TestPeerSeekable_Size_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileInfo(mock.Anything, mock.Anything).Return(&orchestrator.GetBuildFileInfoResponse{NotAvailable: true}, nil)

	baseSeekable := storagemocks.NewMockSeekable(t)
	baseSeekable.EXPECT().Size(mock.Anything).Return(int64(8192), nil)

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{
		client:      client,
		base:        base,
		basePath:    "build-1/memfile",
		baseObjType: storage.MemfileObjectType,
		buildID:     "build-1",
		fileName:    storage.MemfileName,
	}
	size, err := s.Size(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(8192), size)
}

func TestPeerSeekable_ReadAt_PeerSucceeds(t *testing.T) {
	t.Parallel()

	data := []byte("block data")
	stream := orchestratormocks.NewMockChunkService_GetBuildFileClient(t)
	// ReadAt copies the first message directly into buf; the inner loop is skipped when buf is full.
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{Data: data}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileRequest) bool {
		return req.GetOffset() == 0 && req.GetLength() == int64(len(data))
	})).Return(stream, nil)

	s := &peerSeekable{client: client, buildID: "build-1", fileName: storage.MemfileName}
	buf := make([]byte, len(data))
	n, err := s.ReadAt(context.Background(), buf, 0)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, buf)
}

func TestPeerSeekable_ReadAt_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	baseData := []byte("base data")
	stream := orchestratormocks.NewMockChunkService_GetBuildFileClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{NotAvailable: true}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.Anything).Return(stream, nil)

	baseSeekable := storagemocks.NewMockSeekable(t)
	baseSeekable.EXPECT().ReadAt(mock.Anything, mock.Anything, int64(0)).RunAndReturn(func(_ context.Context, buf []byte, _ int64) (int, error) {
		n := copy(buf, baseData)

		return n, nil
	})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{
		client:      client,
		base:        base,
		basePath:    "build-1/memfile",
		baseObjType: storage.MemfileObjectType,
		buildID:     "build-1",
		fileName:    storage.MemfileName,
	}
	buf := make([]byte, len(baseData))
	n, err := s.ReadAt(context.Background(), buf, 0)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), n)
	assert.Equal(t, baseData, buf)
}

func TestPeerSeekable_OpenRangeReader_PeerSucceeds(t *testing.T) {
	t.Parallel()

	data := []byte("range data")
	stream := orchestratormocks.NewMockChunkService_GetBuildFileClient(t)
	// OpenRangeReader reads the first message; peerStreamReader.Read calls Recv once more for EOF.
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{Data: data}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileRequest) bool {
		return req.GetOffset() == 10 && req.GetLength() == int64(len(data))
	})).Return(stream, nil)

	s := &peerSeekable{client: client, buildID: "build-1", fileName: storage.MemfileName}
	rc, err := s.OpenRangeReader(context.Background(), 10, int64(len(data)))
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestPeerSeekable_OpenRangeReader_PeerError_FallsBackToBase(t *testing.T) {
	t.Parallel()

	baseData := []byte("base range")
	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.Anything).Return(nil, errors.New("peer unavailable"))

	baseSeekable := storagemocks.NewMockSeekable(t)
	baseSeekable.EXPECT().OpenRangeReader(mock.Anything, int64(0), int64(len(baseData))).Return(io.NopCloser(bytes.NewReader(baseData)), nil)

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{
		client:      client,
		base:        base,
		basePath:    "build-1/memfile",
		baseObjType: storage.MemfileObjectType,
		buildID:     "build-1",
		fileName:    storage.MemfileName,
	}
	rc, err := s.OpenRangeReader(context.Background(), 0, int64(len(baseData)))
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, baseData, got)
}

// --- newPeerStorageProvider ---

func TestNewPeerStorageProvider_OpenBlob_ExtractsFileName(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildFileClient(t)
	// WriteTo reads first message in WriteTo, then collectStream loops until EOF.
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFileResponse{Data: []byte("data")}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFile(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "snapfile"
	})).Return(stream, nil)

	// Peer succeeds — base blob methods must NOT be called.
	base := storagemocks.NewMockStorageProvider(t)

	p := newPeerStorageProvider(base, client)
	blob, err := p.OpenBlob(context.Background(), "build-1/snapfile", storage.SnapfileObjectType)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = blob.WriteTo(context.Background(), &buf)
	require.NoError(t, err)
	assert.Equal(t, "data", buf.String())
}

func TestNewPeerStorageProvider_OpenSeekable_ExtractsFileName(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileInfo(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileInfoRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "memfile"
	})).Return(&orchestrator.GetBuildFileInfoResponse{TotalSize: 512}, nil)

	// Peer succeeds — base seekable methods must NOT be called.
	base := storagemocks.NewMockStorageProvider(t)

	p := newPeerStorageProvider(base, client)
	seekable, err := p.OpenSeekable(context.Background(), "build-1/memfile", storage.MemfileObjectType)
	require.NoError(t, err)

	size, err := seekable.Size(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(512), size)
}
