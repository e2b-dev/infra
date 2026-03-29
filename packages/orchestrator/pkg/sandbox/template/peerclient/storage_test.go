package peerclient

import (
	"bytes"
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

func TestPeerStorageProvider_OpenBlob_ExtractsFileName(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildBlobClient(t)
	stream.EXPECT().Recv().Return(&orchestrator.GetBuildBlobResponse{Data: []byte("data")}, nil).Once()
	stream.EXPECT().Recv().Return(nil, io.EOF).Once()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildBlob(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildBlobRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "snapfile"
	})).Return(stream, nil)

	base := storage.NewMockStorageProvider(t)

	p := newPeerStorageProvider(base, client, &atomic.Pointer[UploadedHeaders]{})
	blob, err := p.OpenBlob(t.Context(), "build-1/snapfile", storage.SnapfileObjectType)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = blob.WriteTo(t.Context(), &buf)
	require.NoError(t, err)
	assert.Equal(t, "data", buf.String())
}

func TestPeerStorageProvider_OpenFramedFile_ExtractsFileName(t *testing.T) {
	t.Parallel()

	client := orchestratormocks.NewMockChunkServiceClient(t)
	client.EXPECT().GetBuildFileSize(mock.Anything, mock.MatchedBy(func(req *orchestrator.GetBuildFileSizeRequest) bool {
		return req.GetBuildId() == "build-1" && req.GetFileName() == "memfile"
	})).Return(&orchestrator.GetBuildFileSizeResponse{TotalSize: 512}, nil)

	base := storage.NewMockStorageProvider(t)

	p := newPeerStorageProvider(base, client, &atomic.Pointer[UploadedHeaders]{})
	ff, err := p.OpenFramedFile(t.Context(), "build-1/memfile")
	require.NoError(t, err)

	size, err := ff.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(512), size)
}
