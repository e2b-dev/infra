package peerclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratormocks "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestPeerSeekable_Size_PeerSucceeds(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileSizeRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == storage.MemfileName
	})).Return(&orchestrator.GetBuildFileSizeResponse{TotalSize: 4096}, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Pointer[UploadedHeaders]{}}}
	size, err := s.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(4096), size)
}

func TestPeerSeekable_Size_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.Anything).Return(&orchestrator.GetBuildFileSizeResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil)

	baseSeekable := storage.NewMockSeekable(t)
	baseSeekable.EXPECT().Size(mock.Anything).Return(int64(8192), nil)

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Pointer[UploadedHeaders]{},
		openFn: func(ctx context.Context) (storage.Seekable, error) {
			return base.OpenSeekable(ctx, "build-1/memfile", storage.MemfileObjectType)
		},
	}}
	size, err := s.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(8192), size)
}

func TestPeerSeekable_ReadAt_PeerSucceeds(t *testing.T) {
	t.Parallel()

	data := []byte("block data")
	stream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	// ReadAt copies the first message directly into buf; the inner loop is skipped when buf is full.
	stream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{Data: data}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.MatchedBy(func(req *orchestrator.ReadAtBuildSeekableRequest) bool {
		return req.GetOffset() == 0 && req.GetLength() == int64(len(data))
	})).Return(stream, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Pointer[UploadedHeaders]{}}}
	buf := make([]byte, len(data))
	n, err := s.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, buf)
}

func TestPeerSeekable_ReadAt_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	baseData := []byte("base data")
	stream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.Anything).Return(stream, nil)

	baseSeekable := storage.NewMockSeekable(t)
	baseSeekable.EXPECT().OpenRangeReader(mock.Anything, int64(0), int64(len(baseData)), (*storage.FrameTable)(nil)).
		Return(io.NopCloser(bytes.NewReader(baseData)), nil)

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Pointer[UploadedHeaders]{},
		openFn: func(ctx context.Context) (storage.Seekable, error) {
			return base.OpenSeekable(ctx, "build-1/memfile", storage.MemfileObjectType)
		},
	}}
	buf := make([]byte, len(baseData))
	n, err := s.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), n)
	assert.Equal(t, baseData, buf)
}

func TestPeerSeekable_OpenRangeReader_PeerSucceeds(t *testing.T) {
	t.Parallel()

	data := []byte("range data")
	stream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	// OpenRangeReader reads the first message; peerStreamReader.Read calls Recv once more for EOF.
	stream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{Data: data}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.MatchedBy(func(req *orchestrator.ReadAtBuildSeekableRequest) bool {
		return req.GetOffset() == 10 && req.GetLength() == int64(len(data))
	})).Return(stream, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Pointer[UploadedHeaders]{}}}
	rc, err := s.OpenRangeReader(t.Context(), 10, int64(len(data)), nil)
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
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.Anything).Return(nil, errors.New("peer unavailable"))

	baseSeekable := storage.NewMockSeekable(t)
	baseSeekable.EXPECT().OpenRangeReader(mock.Anything, int64(0), int64(len(baseData)), (*storage.FrameTable)(nil)).Return(io.NopCloser(bytes.NewReader(baseData)), nil)

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Pointer[UploadedHeaders]{},
		openFn: func(ctx context.Context) (storage.Seekable, error) {
			return base.OpenSeekable(ctx, "build-1/memfile", storage.MemfileObjectType)
		},
	}}
	rc, err := s.OpenRangeReader(t.Context(), 0, int64(len(baseData)), nil)
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, baseData, got)
}

func TestPeerSeekable_OpenRangeReader_UploadedHeaders_ReturnsPeerTransitionedError(t *testing.T) {
	t.Parallel()

	memHeader := []byte("mem-header-v4")
	rootHeader := []byte("root-header-v4")

	client := orchestratormocks.NewMockChunkServiceClient(t)

	uploaded := &atomic.Pointer[UploadedHeaders]{}
	uploaded.Store(&UploadedHeaders{
		MemfileHeader: memHeader,
		RootfsHeader:  rootHeader,
	})

	baseSeekable := storage.NewMockSeekable(t)
	base := storage.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: uploaded,
		openFn: func(ctx context.Context) (storage.Seekable, error) {
			return base.OpenSeekable(ctx, "build-1/memfile", storage.MemfileObjectType)
		},
	}}

	// frameTable=nil triggers the transition header check in the fallback path
	_, err := s.OpenRangeReader(t.Context(), 0, 100, nil)
	require.Error(t, err)

	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)
	assert.Equal(t, memHeader, transErr.MemfileHeader)
	assert.Equal(t, rootHeader, transErr.RootfsHeader)
}

func TestPeerSeekable_OpenRangeReader_UploadedSkipsPeer(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)

	uploaded := &atomic.Pointer[UploadedHeaders]{}
	uploaded.Store(&UploadedHeaders{})

	baseData := []byte("from gcs")
	baseSeekable := storage.NewMockSeekable(t)
	baseSeekable.EXPECT().OpenRangeReader(mock.Anything, int64(0), int64(len(baseData)), (*storage.FrameTable)(nil)).Return(io.NopCloser(bytes.NewReader(baseData)), nil)

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).Return(baseSeekable, nil)

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: uploaded,
		openFn: func(ctx context.Context) (storage.Seekable, error) {
			return base.OpenSeekable(ctx, "build-1/memfile", storage.MemfileObjectType)
		},
	}}

	rc, err := s.OpenRangeReader(t.Context(), 0, int64(len(baseData)), nil)
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, baseData, got)
}
