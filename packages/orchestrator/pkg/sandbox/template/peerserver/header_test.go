package peerserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blockmocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/mocks"
	templatemocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/mocks"
	peerservermocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerserver/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	storageheader "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestHeaderSource_Stream(t *testing.T) {
	t.Parallel()

	h, err := storageheader.NewHeader(&storageheader.Metadata{Version: 3, BlockSize: 4096, Size: 4096}, nil)
	require.NoError(t, err)

	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(h)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Memfile(mock.Anything).Return(dev, nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().GetCachedTemplate("build-1").Return(tmplMock, true)

	src, err := ResolveBlob(cache, "build-1", storage.MemfileName+storage.HeaderSuffix)
	require.NoError(t, err)

	sender := &collectSender{}

	require.NoError(t, src.Stream(t.Context(), sender))
	assert.NotEmpty(t, sender.data)
}

func TestHeaderSource_Stream_NilHeader(t *testing.T) {
	t.Parallel()

	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(nil)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Memfile(mock.Anything).Return(dev, nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().GetCachedTemplate("build-1").Return(tmplMock, true)

	src, err := ResolveBlob(cache, "build-1", storage.MemfileName+storage.HeaderSuffix)
	require.NoError(t, err)

	err = src.Stream(t.Context(), &collectSender{})
	assert.ErrorIs(t, err, ErrNotAvailable)
}

func TestHeaderSource_Stream_Rootfs(t *testing.T) {
	t.Parallel()

	h, err := storageheader.NewHeader(&storageheader.Metadata{Version: 3, BlockSize: 4096, Size: 4096}, nil)
	require.NoError(t, err)

	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(h)

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Rootfs().Return(dev, nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().GetCachedTemplate("build-1").Return(tmplMock, true)

	src, err := ResolveBlob(cache, "build-1", storage.RootfsName+storage.HeaderSuffix)
	require.NoError(t, err)

	sender := &collectSender{}

	require.NoError(t, src.Stream(t.Context(), sender))
	assert.NotEmpty(t, sender.data)
}
