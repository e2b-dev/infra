package peerserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	buildmocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build/mocks"
	peerservermocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerserver/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestSeekableSource_Size(t *testing.T) {
	t.Parallel()

	diff := buildmocks.NewMockDiff(t)
	diff.EXPECT().FileSize().Return(int64(1234), nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().LookupDiff("build-1", build.DiffType(storage.MemfileName)).Return(diff, true)

	src, err := ResolveSeekable(cache, "build-1", storage.MemfileName)
	require.NoError(t, err)

	size, err := src.Size(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1234), size)
}

func TestSeekableSource_Stream(t *testing.T) {
	t.Parallel()

	data := []byte("diff bytes")

	diff := buildmocks.NewMockDiff(t)
	diff.EXPECT().Block(mock.Anything, int64(0), (*storage.FrameTable)(nil)).Return(data, nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().LookupDiff("build-1", build.DiffType(storage.MemfileName)).Return(diff, true)

	src, err := ResolveSeekable(cache, "build-1", storage.MemfileName)
	require.NoError(t, err)

	sender := &collectSender{}
	err = src.Stream(t.Context(), 0, int64(len(data)), sender)
	require.NoError(t, err)
	assert.Equal(t, data, sender.data)
}

func TestSeekableSource_Stream_MultiBlock(t *testing.T) {
	t.Parallel()

	// 3 full blocks (4 bytes each) + partial last block.
	fullData := []byte("AAAABBBBCCCCdd")

	diff := buildmocks.NewMockDiff(t)
	diff.EXPECT().Block(mock.Anything, int64(0), (*storage.FrameTable)(nil)).Return(fullData[0:4], nil)
	diff.EXPECT().Block(mock.Anything, int64(4), (*storage.FrameTable)(nil)).Return(fullData[4:8], nil)
	diff.EXPECT().Block(mock.Anything, int64(8), (*storage.FrameTable)(nil)).Return(fullData[8:12], nil)
	diff.EXPECT().Block(mock.Anything, int64(12), (*storage.FrameTable)(nil)).Return(fullData[12:14], nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().LookupDiff("build-1", build.DiffType(storage.MemfileName)).Return(diff, true)

	src, err := ResolveSeekable(cache, "build-1", storage.MemfileName)
	require.NoError(t, err)

	sender := &collectSender{}
	err = src.Stream(t.Context(), 0, int64(len(fullData)), sender)
	require.NoError(t, err)
	require.Equal(t, fullData, sender.data)
}
