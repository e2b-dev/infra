package peerclient

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratormocks "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
)

func TestPeerFramedFile_Size_PeerSucceeds(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileSizeRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == storage.MemfileName
	})).Return(&orchestrator.GetBuildFileSizeResponse{TotalSize: 4096}, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
	}}
	size, err := f.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(4096), size)
}

func TestPeerFramedFile_Size_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.Anything).Return(
		&orchestrator.GetBuildFileSizeResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil)

	baseFF := storagemocks.NewMockFramedFile(t)
	baseFF.EXPECT().Size(mock.Anything).Return(int64(8192), nil)

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}
	size, err := f.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(8192), size)
}

func TestPeerFramedFile_GetFrame_PeerSucceeds(t *testing.T) {
	t.Parallel()

	data := []byte("block data")
	stream := orchestratormocks.NewMockChunkService_GetBuildFrameClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFrameResponse{Data: data}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFrame(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFrameRequest) bool {
		return req.GetOffset() == 0 && req.GetLength() == int64(len(data))
	})).Return(stream, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
	}}
	buf := make([]byte, len(data))
	r, err := f.GetFrame(t.Context(), 0, nil, false, buf, int64(len(data)), nil)
	require.NoError(t, err)
	assert.Equal(t, len(data), r.Length)
	assert.Equal(t, data, buf[:r.Length])
}

func TestPeerFramedFile_GetFrame_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	baseData := []byte("base data")
	stream := orchestratormocks.NewMockChunkService_GetBuildFrameClient(t)
	stream.EXPECT().Recv().Return(
		&orchestrator.GetBuildFrameResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFrame(mock.Anything, mock.Anything).Return(stream, nil)

	baseFF := storagemocks.NewMockFramedFile(t)
	baseFF.EXPECT().GetFrame(mock.Anything, int64(0), (*storage.FrameTable)(nil), false, mock.Anything, int64(len(baseData)), mock.Anything).
		RunAndReturn(func(_ context.Context, _ int64, _ *storage.FrameTable, _ bool, buf []byte, _ int64, onRead func(int64)) (storage.Range, error) {
			n := copy(buf, baseData)
			if onRead != nil {
				onRead(int64(n))
			}

			return storage.Range{Start: 0, Length: n}, nil
		})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}
	buf := make([]byte, len(baseData))
	r, err := f.GetFrame(t.Context(), 0, nil, false, buf, int64(len(baseData)), nil)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), r.Length)
	assert.Equal(t, baseData, buf[:r.Length])
}

func TestPeerFramedFile_GetFrame_PeerError_FallsBackToBase(t *testing.T) {
	t.Parallel()

	baseData := []byte("fallback")
	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFrame(mock.Anything, mock.Anything).Return(nil, errors.New("peer unavailable"))

	baseFF := storagemocks.NewMockFramedFile(t)
	baseFF.EXPECT().GetFrame(mock.Anything, int64(0), (*storage.FrameTable)(nil), false, mock.Anything, int64(len(baseData)), mock.Anything).
		RunAndReturn(func(_ context.Context, _ int64, _ *storage.FrameTable, _ bool, buf []byte, _ int64, onRead func(int64)) (storage.Range, error) {
			n := copy(buf, baseData)
			if onRead != nil {
				onRead(int64(n))
			}

			return storage.Range{Start: 0, Length: n}, nil
		})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}
	buf := make([]byte, len(baseData))
	r, err := f.GetFrame(t.Context(), 0, nil, false, buf, int64(len(baseData)), nil)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), r.Length)
	assert.Equal(t, baseData, buf[:r.Length])
}

func TestPeerFramedFile_GetFrame_OnReadCallback(t *testing.T) {
	t.Parallel()

	data := []byte("callback test")
	stream := orchestratormocks.NewMockChunkService_GetBuildFrameClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFrameResponse{Data: data}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFrame(mock.Anything, mock.Anything).Return(stream, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
	}}

	var reported int64
	buf := make([]byte, len(data))
	r, err := f.GetFrame(t.Context(), 0, nil, false, buf, int64(len(data)), func(n int64) { reported = n })
	require.NoError(t, err)
	assert.Equal(t, len(data), r.Length)
	assert.Equal(t, int64(len(data)), reported)
}

func TestPeerFramedFile_GetFrame_PartialStreamError(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildFrameClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildFrameResponse{Data: []byte("part")}, nil).Once()
	stream.EXPECT().Recv().Return(nil, fmt.Errorf("connection reset")).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFrame(mock.Anything, mock.Anything).Return(stream, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: &atomic.Bool{},
	}}
	buf := make([]byte, 100)
	r, err := f.GetFrame(t.Context(), 0, nil, false, buf, 100, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to receive chunk from peer")
	assert.Equal(t, 4, r.Length)
}

func TestPeerFramedFile_Size_UseStorage_SetsUploadedAndStoresTransitionHeaders(t *testing.T) {
	t.Parallel()

	memHeader := []byte("mem-header-v4")
	rootHeader := []byte("root-header-v4")

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.Anything).Return(
		&orchestrator.GetBuildFileSizeResponse{
			Availability: &orchestrator.PeerAvailability{
				UseStorage:    true,
				MemfileHeader: memHeader,
				RootfsHeader:  rootHeader,
			},
		}, nil)

	baseFF := storagemocks.NewMockFramedFile(t)
	baseFF.EXPECT().Size(mock.Anything).Return(int64(4096), nil)

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	uploaded := &atomic.Bool{}
	transHdrs := &atomic.Pointer[TransitionHeaders]{}

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:            client,
		buildID:           "build-1",
		fileName:          storage.MemfileName,
		uploaded:          uploaded,
		transitionHeaders: transHdrs,
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}

	size, err := f.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(4096), size)
	assert.True(t, uploaded.Load(), "uploaded flag should be set")

	hdrs := transHdrs.Load()
	require.NotNil(t, hdrs, "transition headers should be stored")
	assert.Equal(t, memHeader, hdrs.MemfileHeader)
	assert.Equal(t, rootHeader, hdrs.RootfsHeader)
}

func TestPeerFramedFile_GetFrame_TransitionHeaders_ReturnsPeerTransitionedError(t *testing.T) {
	t.Parallel()

	memHeader := []byte("mem-header-v4")
	rootHeader := []byte("root-header-v4")

	client := orchestratormocks.NewMockChunkServiceClient(t)

	uploaded := &atomic.Bool{}
	uploaded.Store(true)

	transHdrs := &atomic.Pointer[TransitionHeaders]{}
	transHdrs.Store(&TransitionHeaders{
		MemfileHeader: memHeader,
		RootfsHeader:  rootHeader,
	})

	baseFF := storagemocks.NewMockFramedFile(t)
	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:            client,
		buildID:           "build-1",
		fileName:          storage.MemfileName,
		uploaded:          uploaded,
		transitionHeaders: transHdrs,
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}

	buf := make([]byte, 100)
	// frameTable=nil triggers the transition header check in the fallback path
	_, err := f.GetFrame(t.Context(), 0, nil, false, buf, 100, nil)
	require.Error(t, err)

	var transErr *storage.PeerTransitionedError
	require.ErrorAs(t, err, &transErr)
	assert.Equal(t, memHeader, transErr.MemfileHeader)
	assert.Equal(t, rootHeader, transErr.RootfsHeader)
}

func TestPeerFramedFile_GetFrame_WithFrameTable_NoTransitionError(t *testing.T) {
	t.Parallel()

	// When frameTable is non-nil, the fallback should call base.GetFrame
	// directly without checking transition headers.
	client := orchestratormocks.NewMockChunkServiceClient(t)

	uploaded := &atomic.Bool{}
	uploaded.Store(true)

	transHdrs := &atomic.Pointer[TransitionHeaders]{}
	transHdrs.Store(&TransitionHeaders{
		MemfileHeader: []byte("mem"),
		RootfsHeader:  []byte("root"),
	})

	ft := &storage.FrameTable{}
	baseData := []byte("compressed data")

	baseFF := storagemocks.NewMockFramedFile(t)
	baseFF.EXPECT().GetFrame(mock.Anything, int64(0), ft, true, mock.Anything, int64(len(baseData)), mock.Anything).
		RunAndReturn(func(_ context.Context, _ int64, _ *storage.FrameTable, _ bool, buf []byte, _ int64, onRead func(int64)) (storage.Range, error) {
			n := copy(buf, baseData)
			if onRead != nil {
				onRead(int64(n))
			}

			return storage.Range{Start: 0, Length: n}, nil
		})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:            client,
		buildID:           "build-1",
		fileName:          storage.MemfileName,
		uploaded:          uploaded,
		transitionHeaders: transHdrs,
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}

	buf := make([]byte, len(baseData))
	r, err := f.GetFrame(t.Context(), 0, ft, true, buf, int64(len(baseData)), nil)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), r.Length)
	assert.Equal(t, baseData, buf[:r.Length])
}

func TestPeerFramedFile_GetFrame_UploadedSkipsPeer(t *testing.T) {
	t.Parallel()

	// When uploaded=true, withPeerFallback skips the peer entirely.
	client := orchestratormocks.NewMockChunkServiceClient(t)
	// No expectations on client — it should not be called.

	uploaded := &atomic.Bool{}
	uploaded.Store(true)

	baseData := []byte("from gcs")
	baseFF := storagemocks.NewMockFramedFile(t)
	baseFF.EXPECT().GetFrame(mock.Anything, int64(0), (*storage.FrameTable)(nil), false, mock.Anything, int64(len(baseData)), mock.Anything).
		RunAndReturn(func(_ context.Context, _ int64, _ *storage.FrameTable, _ bool, buf []byte, _ int64, onRead func(int64)) (storage.Range, error) {
			n := copy(buf, baseData)
			if onRead != nil {
				onRead(int64(n))
			}

			return storage.Range{Start: 0, Length: n}, nil
		})

	base := storagemocks.NewMockStorageProvider(t)
	base.EXPECT().OpenFramedFile(mock.Anything, "build-1/memfile").Return(baseFF, nil)

	f := &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   client,
		buildID:  "build-1",
		fileName: storage.MemfileName,
		uploaded: uploaded,
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return base.OpenFramedFile(ctx, "build-1/memfile")
		},
	}}

	buf := make([]byte, len(baseData))
	r, err := f.GetFrame(t.Context(), 0, nil, false, buf, int64(len(baseData)), nil)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), r.Length)
	assert.Equal(t, baseData, buf[:r.Length])
}
