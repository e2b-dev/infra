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
		return req.GetBuildId() == "build-1" && req.GetName() == storage.MemfileName
	})).Return(&orchestrator.GetBuildFileSizeResponse{TotalSize: 4096}, nil)

	s := &peerSeekable{peerHandle: peerHandle{client: client, buildID: "build-1", name: storage.MemfileName, uploaded: &atomic.Bool{}}}
	size, err := s.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(4096), size)
}

// Size has no caller-provided FT and cannot guess the CT, so a peer miss must
// emit PeerTransitionedError so the caller refreshes against the authoritative
// header rather than 404ing against a basic-name fall-through on compressed
// builds. base must NOT be touched.
func TestPeerSeekable_Size_PeerNotAvailable_EmitsPeerTransitionedError(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.Anything).Return(&orchestrator.GetBuildFileSizeResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil)

	// NewMockStorageProvider auto-fails on any unexpected call.
	base := storage.NewMockStorageProvider(t)

	s := &peerSeekable{
		peerHandle: peerHandle{
			client:   client,
			buildID:  "build-1",
			name:     storage.MemfileName,
			uploaded: &atomic.Bool{},
		},
		basePersistence: base,
		objType:         storage.MemfileObjectType,
	}
	_, err := s.Size(t.Context())
	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)
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

	s := &peerSeekable{peerHandle: peerHandle{client: client, buildID: "build-1", name: storage.MemfileName, uploaded: &atomic.Bool{}}}
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
			name:     storage.MemfileName,
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
			name:     storage.MemfileName,
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

// TestPeerStorageProvider_TransitionEmitsError covers the peer→storage
// transition contract: while uploaded is false, peerSeekable serves from the
// peer; the call that observes UseStorage flips uploaded and returns its
// bytes; the NEXT call on the same wrapper returns PeerTransitionedError
// without touching peer or base. The wrapper never falls through to base
// post-transition — that's the resolver's job (attrResolveUploaded) once the
// caller reopens. Catching a peerSeekable falling through here would mean we
// regressed to the 404-driven recovery design.
func TestPeerStorageProvider_TransitionEmitsError(t *testing.T) {
	t.Parallel()

	uploaded := &atomic.Bool{}
	prePeerBytes := []byte("pre-transition peer payload")

	preStream := orchestratormocks.NewMockChunkService_ReadAtBuildSeekableClient(t)
	preStream.EXPECT().Recv().Return(&orchestrator.ReadAtBuildSeekableResponse{Data: prePeerBytes}, nil).Once()
	preStream.EXPECT().Recv().RunAndReturn(func() (*orchestrator.ReadAtBuildSeekableResponse, error) {
		uploaded.Store(true)

		return nil, io.EOF
	}).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().ReadAtBuildSeekable(mock.Anything, mock.MatchedBy(func(req *orchestrator.ReadAtBuildSeekableRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetName() == storage.MemfileName
	})).Return(preStream, nil).Once()

	// base must NOT be touched: any post-transition fall-through would be a
	// regression to 404-driven recovery. NewMockStorageProvider(t) auto-asserts
	// no unexpected calls on cleanup.
	base := storage.NewMockStorageProvider(t)

	p := newPeerStorageProvider(base, client, uploaded)
	seekable, err := p.OpenSeekable(t.Context(), "build-1/memfile", storage.MemfileObjectType)
	require.NoError(t, err)

	rc, err := seekable.OpenRangeReader(t.Context(), 0, int64(len(prePeerBytes)),
		storage.NewFullFrameTable(storage.CompressionNone, nil).Table())
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, prePeerBytes, got)
	require.True(t, uploaded.Load(), "uploaded flag should be set after peer EOF with UseStorage")

	_, err = seekable.OpenRangeReader(t.Context(), 0, 1,
		storage.NewFullFrameTable(storage.CompressionNone, nil).Table())
	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)
}
