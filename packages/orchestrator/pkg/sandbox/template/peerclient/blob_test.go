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
	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
	providermocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks/provider"
)

func TestPeerBlob_WriteTo_PeerSucceeds(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildBlobClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildBlobResponse{Data: []byte("hello ")}, nil).Once()
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildBlobResponse{Data: []byte("world")}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildBlob(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildBlobRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "snapfile"
	})).Return(stream, nil)

	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
		uploaded: &atomic.Pointer[UploadedHeaders]{},
	}}

	var buf bytes.Buffer
	n, err := blob.WriteTo(t.Context(), &buf)
	require.NoError(t, err)
	assert.Equal(t, int64(11), n)
	assert.Equal(t, "hello world", buf.String())
}

func TestPeerBlob_WriteTo_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildBlobClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildBlobResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildBlob(mock.Anything, mock.Anything).Return(stream, nil)

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().WriteTo(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
		n, err := dst.Write([]byte("from gcs"))

		return int64(n), err
	})
	base := providermocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile").Return(baseBlob, nil)

	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
		uploaded: &atomic.Pointer[UploadedHeaders]{},
		openFn: func(ctx context.Context) (storage.Blob, error) {
			return base.OpenBlob(ctx, "build-1/snapfile")
		},
	}}

	var buf bytes.Buffer
	n, err := blob.WriteTo(t.Context(), &buf)
	require.NoError(t, err)
	assert.Equal(t, int64(8), n)
	assert.Equal(t, "from gcs", buf.String())
}

func TestPeerBlob_WriteTo_PeerError_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildBlob(mock.Anything, mock.Anything).Return(nil, errors.New("connection refused"))

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().WriteTo(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
		n, err := dst.Write([]byte("from gcs"))

		return int64(n), err
	})
	base := providermocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile").Return(baseBlob, nil)

	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
		uploaded: &atomic.Pointer[UploadedHeaders]{},
		openFn: func(ctx context.Context) (storage.Blob, error) {
			return base.OpenBlob(ctx, "build-1/snapfile")
		},
	}}

	var buf bytes.Buffer
	_, err := blob.WriteTo(t.Context(), &buf)
	require.NoError(t, err)
	assert.Equal(t, "from gcs", buf.String())
}

func TestPeerBlob_WriteTo_UploadedSetMidStream_CompletesFromPeerThenFallsBack(t *testing.T) {
	t.Parallel()

	uploaded := &atomic.Pointer[UploadedHeaders]{}

	// Peer streams three chunks; the second Recv sets uploaded=true
	// (simulating a concurrent operation receiving UseStorage).
	stream := orchestratormocks.NewMockChunkService_GetBuildBlobClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildBlobResponse{Data: []byte("aaa")}, nil).Once()
	stream.EXPECT().Recv().RunAndReturn(func() (*orchestrator.GetBuildBlobResponse, error) {
		uploaded.Store(&UploadedHeaders{})

		return &orchestrator.GetBuildBlobResponse{Data: []byte("bbb")}, nil
	}).Once()
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildBlobResponse{Data: []byte("ccc")}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildBlob(mock.Anything, mock.Anything).Return(stream, nil).Once()

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().WriteTo(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
		n, err := dst.Write([]byte("from storage"))

		return int64(n), err
	})
	base := providermocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile").Return(baseBlob, nil)

	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
		uploaded: uploaded,
		openFn: func(ctx context.Context) (storage.Blob, error) {
			return base.OpenBlob(ctx, "build-1/snapfile")
		},
	}}

	// First download: in-flight stream completes from peer despite uploaded being set mid-stream.
	var buf1 bytes.Buffer
	n1, err := blob.WriteTo(t.Context(), &buf1)
	require.NoError(t, err)
	assert.Equal(t, int64(9), n1)
	assert.Equal(t, "aaabbbccc", buf1.String())
	assert.NotNil(t, uploaded.Load())

	// Second download: uploaded is now true, skips peer and goes to base storage.
	var buf2 bytes.Buffer
	n2, err := blob.WriteTo(t.Context(), &buf2)
	require.NoError(t, err)
	assert.Equal(t, int64(12), n2)
	assert.Equal(t, "from storage", buf2.String())
}

func TestPeerBlob_Exists_PeerHasFile(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileExists(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileExistsRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "snapfile"
	})).Return(&orchestrator.GetBuildFileExistsResponse{}, nil)

	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{client: client, buildID: "build-1", fileName: "snapfile", uploaded: &atomic.Pointer[UploadedHeaders]{}}}
	ok, err := blob.Exists(t.Context())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPeerBlob_Exists_PeerNotAvailable_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileExists(mock.Anything, mock.Anything).Return(&orchestrator.GetBuildFileExistsResponse{Availability: &orchestrator.PeerAvailability{NotAvailable: true}}, nil)

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().Exists(mock.Anything).Return(true, nil)
	base := providermocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile").Return(baseBlob, nil)

	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
		uploaded: &atomic.Pointer[UploadedHeaders]{},
		openFn: func(ctx context.Context) (storage.Blob, error) {
			return base.OpenBlob(ctx, "build-1/snapfile")
		},
	}}

	ok, err := blob.Exists(t.Context())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPeerBlob_Exists_UseStorage_FallsBackToBase(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileExists(mock.Anything, mock.Anything).Return(&orchestrator.GetBuildFileExistsResponse{Availability: &orchestrator.PeerAvailability{UseStorage: true}}, nil)

	baseBlob := storagemocks.NewMockBlob(t)
	baseBlob.EXPECT().Exists(mock.Anything).Return(true, nil)
	base := providermocks.NewMockStorageProvider(t)
	base.EXPECT().OpenBlob(mock.Anything, "build-1/snapfile").Return(baseBlob, nil)

	uploaded := &atomic.Pointer[UploadedHeaders]{}
	blob := &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   client,
		buildID:  "build-1",
		fileName: "snapfile",
		uploaded: uploaded,
		openFn: func(ctx context.Context) (storage.Blob, error) {
			return base.OpenBlob(ctx, "build-1/snapfile")
		},
	}}

	ok, err := blob.Exists(t.Context())
	require.NoError(t, err)
	assert.True(t, ok)
	assert.NotNil(t, uploaded.Load(), "uploaded flag should be set after UseStorage response")
}
