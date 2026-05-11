package peerclient

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratormocks "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestPeerSeekable_Size_PeerSucceeds(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileSizeRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetName() == storage.MemfileName
	})).Return(&orchestrator.GetBuildFileSizeResponse{TotalSize: 4096}, nil)

	s := &peerSeekable{peerHandle: peerHandle{client: client, buildID: "build-1", name: storage.MemfileName, state: &peerState{}}}
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

	s := &peerSeekable{
		peerHandle: peerHandle{
			client:  client,
			buildID: "build-1",
			name:    storage.MemfileName,
			state:   &peerState{},
		},
		basePersistence: base,
		objType:         storage.MemfileObjectType,
	}
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

	s := &peerSeekable{peerHandle: peerHandle{client: client, buildID: "build-1", name: storage.MemfileName, state: &peerState{}}}
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

	s := &peerSeekable{
		peerHandle: peerHandle{
			client:  client,
			buildID: "build-1",
			name:    storage.MemfileName,
			state:   &peerState{},
		},
		basePersistence: base,
		objType:         storage.MemfileObjectType,
	}
	rc, err := s.OpenRangeReader(t.Context(), 0, int64(len(baseData)), nil)
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, baseData, got)
}

func TestPeerSeekable_OpenRangeReader_Uploaded_RoutesToBase(t *testing.T) {
	t.Parallel()

	// Once uploaded flips, OpenRangeReader skips the peer and routes to base
	// directly. Header coordination is the File's job (via
	// storage.HeaderReloadProbe + ensureHeaderSwapped), not peerSeekable's.
	baseData := []byte("base range")
	client := orchestratormocks.NewMockChunkServiceClient(t)

	state := &peerState{}
	state.uploaded.Store(true)

	baseSeekable := storage.NewMockSeekable(t)
	baseSeekable.EXPECT().
		OpenRangeReader(mock.Anything, int64(0), int64(len(baseData)), mock.Anything).
		Return(io.NopCloser(bytes.NewReader(baseData)), nil).Once()

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().
		OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).
		Return(baseSeekable, nil).Once()

	s := &peerSeekable{
		peerHandle: peerHandle{
			client:  client,
			buildID: "build-1",
			name:    storage.MemfileName,
			state:   state,
		},
		basePersistence: base,
		objType:         storage.MemfileObjectType,
	}

	rc, err := s.OpenRangeReader(t.Context(), 0, int64(len(baseData)),
		storage.NewFrameTable(storage.CompressionNone, nil))
	require.NoError(t, err)
	defer rc.Close()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, baseData, got)
}

func TestPeerStorageProvider_FullTransitionFlow(t *testing.T) {
	t.Parallel()

	state := &peerState{}

	buildUUID := uuid.New()
	v4Header, err := header.NewHeader(&header.Metadata{
		Version:   header.MetadataVersionV4,
		BuildId:   buildUUID,
		BlockSize: 4096,
		Size:      4096,
	}, nil)
	require.NoError(t, err)
	headerBytes, err := header.SerializeHeader(v4Header)
	require.NoError(t, err)

	prePeerBytes := []byte("pre-transition peer payload")
	postBaseBytes := []byte("post-transition compressed payload")

	preStream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	preStream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{Data: prePeerBytes}, nil).Once()
	preStream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{
		Availability: &orchestrator.PeerAvailability{UseStorage: true, HeaderBytes: headerBytes},
	}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.MatchedBy(func(req *orchestrator.ReadAtBuildSeekableRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetName() == storage.MemfileName
	})).Return(preStream, nil).Once()

	postBaseSeekable := storage.NewMockSeekable(t)
	postBaseSeekable.EXPECT().
		OpenRangeReader(mock.Anything, int64(0), int64(len(postBaseBytes)), mock.Anything).
		Return(io.NopCloser(bytes.NewReader(postBaseBytes)), nil).Once()

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().
		OpenSeekable(mock.Anything, "build-1/memfile.zstd", storage.MemfileObjectType).
		Return(postBaseSeekable, nil).Once()

	p := newPeerStorageProvider(base, client, state)
	seekable, err := p.OpenSeekable(t.Context(), "build-1/memfile", storage.MemfileObjectType)
	require.NoError(t, err)

	ftZstd := storage.NewFrameTable(storage.CompressionZstd, nil)
	rc, err := seekable.OpenRangeReader(t.Context(), 0, int64(len(prePeerBytes)), ftZstd)
	require.NoError(t, err)
	_, _ = io.ReadAll(rc)
	require.NoError(t, rc.Close())

	require.True(t, state.uploaded.Load(), "uploaded should be set after UseStorage")
	got := state.header(storage.MemfileName)
	require.NotNil(t, got, "pending header should be stashed")
	require.Equal(t, buildUUID, got.Metadata.BuildId)

	rc, err = seekable.OpenRangeReader(t.Context(), 0, int64(len(postBaseBytes)), ftZstd)
	require.NoError(t, err)
	gotBytes, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, postBaseBytes, gotBytes)
}

// V3 transition: server attaches no header bytes; client must not stash anything.
func TestPeerStorageProvider_V3Transition_NoPendingHeader(t *testing.T) {
	t.Parallel()

	state := &peerState{}

	preStream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	preStream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{
		Availability: &orchestrator.PeerAvailability{UseStorage: true},
	}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.Anything).Return(preStream, nil).Once()

	baseSeekable := storage.NewMockSeekable(t)
	baseSeekable.EXPECT().
		OpenRangeReader(mock.Anything, int64(0), int64(10), mock.Anything).
		Return(io.NopCloser(bytes.NewReader(make([]byte, 10))), nil).Once()

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().
		OpenSeekable(mock.Anything, "build-1/memfile", storage.MemfileObjectType).
		Return(baseSeekable, nil).Once()

	p := newPeerStorageProvider(base, client, state)
	seekable, err := p.OpenSeekable(t.Context(), "build-1/memfile", storage.MemfileObjectType)
	require.NoError(t, err)

	rc, err := seekable.OpenRangeReader(t.Context(), 0, 10,
		storage.NewFrameTable(storage.CompressionNone, nil))
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	require.True(t, state.uploaded.Load())
	require.Nil(t, state.header(storage.MemfileName), "V3 path must not stash a pending header")
}
