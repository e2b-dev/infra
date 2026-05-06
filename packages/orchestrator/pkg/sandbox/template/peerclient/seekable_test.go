package peerclient

import (
	"bytes"
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

	s := &peerSeekable{peerHandle: peerHandle{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Bool{}}}
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
			client:   client,
			buildID:  "build-1",
			fileName: storage.MemfileName,
			uploaded: &atomic.Bool{},
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

	s := &peerSeekable{peerHandle: peerHandle{client: client, buildID: "build-1", fileName: storage.MemfileName, uploaded: &atomic.Bool{}}}
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
			client:   client,
			buildID:  "build-1",
			fileName: storage.MemfileName,
			uploaded: &atomic.Bool{},
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

func TestPeerSeekable_OpenRangeReader_Uploaded_ReturnsPeerTransitionedError(t *testing.T) {
	t.Parallel()

	// Once uploaded flips, the first OpenRangeReader returns
	// PeerTransitionedError without touching either the peer or base. The
	// caller is expected to refresh its header and retry.
	client := orchestratormocks.NewMockChunkServiceClient(t)
	base := storage.NewMockStorageProvider(t)

	uploaded := &atomic.Bool{}
	uploaded.Store(true)

	s := &peerSeekable{
		peerHandle: peerHandle{
			client:   client,
			buildID:  "build-1",
			fileName: storage.MemfileName,
			uploaded: uploaded,
		},
		basePersistence: base,
		objType:         storage.MemfileObjectType,
	}

	_, err := s.OpenRangeReader(t.Context(), 0, 100, nil)
	require.Error(t, err)

	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)
}

// TestPeerStorageProvider_FullTransitionFlow walks the whole peerclient
// surface across a peer→storage transition with a header swap from V3 (basic
// path) to V4 (zstd-compressed path). Regression cover for the bug where the
// post-transition read kept hitting the original uncompressed path.
//
// Sequence:
//  1. Pre-transition: caller passes ft={ct=None}; peer answers; bytes flow.
//  2. Peer signals UseStorage; uploaded flips to true.
//  3. First post-transition call: peerSeekable returns PeerTransitionedError
//     immediately (no peer call, no base open).
//  4. Caller (build.File.retryOnTransition, simulated here) reloads the V4
//     header and retries with ft={ct=Zstd}.
//  5. peerSeekable falls through to base, which opens "build-1/memfile.zstd"
//     (not "build-1/memfile") and serves the compressed bytes.
func TestPeerStorageProvider_FullTransitionFlow(t *testing.T) {
	t.Parallel()

	uploaded := &atomic.Bool{}

	prePeerBytes := []byte("pre-transition peer payload")
	postBaseBytes := []byte("post-transition compressed payload")

	// Pre-transition peer stream: serves bytes once, then EOF. uploaded is
	// flipped via UseStorage on the EOF response so subsequent calls skip
	// the peer.
	preStream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	preStream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{Data: prePeerBytes}, nil).Once()
	preStream.EXPECT().Recv().RunAndReturn(func() (*orchestrator.ReadAtBuildSeekableResponse, error) {
		uploaded.Store(true)

		return nil, io.EOF
	}).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.MatchedBy(func(req *orchestrator.ReadAtBuildSeekableRequest) bool {
		// Peer is asked by basic name only.
		return req.GetBuildId() == "build-1" && req.GetFileName() == storage.MemfileName
	})).Return(preStream, nil).Once()

	// Base is only consulted post-transition, and only against the compressed
	// path. If the bug regresses (uncompressed path), this expectation fails.
	postBaseSeekable := storage.NewMockSeekable(t)
	postBaseSeekable.EXPECT().
		OpenRangeReader(mock.Anything, int64(0), int64(len(postBaseBytes)), mock.Anything).
		Return(io.NopCloser(bytes.NewReader(postBaseBytes)), nil).Once()

	base := storage.NewMockStorageProvider(t)
	base.EXPECT().
		OpenSeekable(mock.Anything, "build-1/memfile.zstd", storage.MemfileObjectType).
		Return(postBaseSeekable, nil).Once()

	p := newPeerStorageProvider(base, client, uploaded)
	seekable, err := p.OpenSeekable(t.Context(), "build-1/memfile", storage.MemfileObjectType)
	require.NoError(t, err)

	// 1. Pre-transition read via peer. ft={ct=None} (V3 header).
	rc, err := seekable.OpenRangeReader(t.Context(), 0, int64(len(prePeerBytes)),
		storage.NewFrameTable(storage.CompressionNone, nil))
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, prePeerBytes, got)
	require.True(t, uploaded.Load(), "uploaded flag should be set after peer EOF with UseStorage")

	// 2. First post-transition call: retriable error, no peer/base contact.
	_, err = seekable.OpenRangeReader(t.Context(), 0, 1,
		storage.NewFrameTable(storage.CompressionNone, nil))
	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)

	// 3. Caller reloads V4 header and retries with ct=Zstd. This must hit the
	//    compressed path on base.
	rc, err = seekable.OpenRangeReader(t.Context(), 0, int64(len(postBaseBytes)),
		storage.NewFrameTable(storage.CompressionZstd, nil))
	require.NoError(t, err)
	got, err = io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, postBaseBytes, got)
}
