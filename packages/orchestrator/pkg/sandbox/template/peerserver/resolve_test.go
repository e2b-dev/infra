package peerserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	peerservermocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerserver/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestResolveFramed_ReturnsErrNotAvailableWhenNotInCache(t *testing.T) {
	t.Parallel()

	for _, fileName := range []string{
		storage.MemfileName,
		storage.RootfsName,
	} {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			cache := peerservermocks.NewMockCache(t)
			cache.EXPECT().LookupDiff(mock.Anything, mock.Anything).Return(nil, false)

			_, err := ResolveFramed(cache, "build-1", fileName)
			assert.ErrorIs(t, err, ErrNotAvailable)
		})
	}
}

func TestResolveFramed_ReturnsErrorForUnknownFile(t *testing.T) {
	t.Parallel()

	cache := peerservermocks.NewMockCache(t)
	_, err := ResolveFramed(cache, "build-1", "unknown.file")
	assert.ErrorIs(t, err, ErrUnknownFile)
}

func TestResolveBlob_ReturnsErrNotAvailableWhenNotInCache(t *testing.T) {
	t.Parallel()

	for _, fileName := range []string{
		storage.SnapfileName,
		storage.MetadataName,
		storage.MemfileName + storage.HeaderSuffix,
		storage.RootfsName + storage.HeaderSuffix,
	} {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			cache := peerservermocks.NewMockCache(t)
			cache.EXPECT().GetCachedTemplate(mock.Anything).Return(nil, false)

			_, err := ResolveBlob(cache, "build-1", fileName)
			assert.ErrorIs(t, err, ErrNotAvailable)
		})
	}
}

func TestResolveBlob_ReturnsErrorForUnknownFile(t *testing.T) {
	t.Parallel()

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().GetCachedTemplate("build-1").Return(nil, true)

	_, err := ResolveBlob(cache, "build-1", "unknown.file")
	assert.ErrorIs(t, err, ErrUnknownFile)
}
