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

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Bool{}}}
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
		uploaded: &atomic.Bool{},
		openFn: func(ctx context.Context) (storage.Seekable, error) {
			return base.OpenSeekable(ctx, "build-1/memfile", storage.MemfileObjectType)
		},
	}}
	size, err := s.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(8192), size)
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

	s := &peerSeekable{peerHandle: peerHandle[storage.Seekable]{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Bool{}}}
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
		uploaded: &atomic.Bool{},
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

func TestPeerSeekable_OpenRangeReader_Uploaded_ReturnsPeerTransitionedError(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)

	uploaded := &atomic.Bool{}
	uploaded.Store(true)

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

	_, err := s.OpenRangeReader(t.Context(), 0, 100, nil)
	require.Error(t, err)

	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)
}
