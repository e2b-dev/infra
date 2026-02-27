package peerprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	templatemocks "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template/mocks"
	peerprovidermocks "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template/peerprovider/mocks"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestMetadataSource_Stream(t *testing.T) {
	t.Parallel()

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Metadata().Return(metadata.Template{
		Template: metadata.TemplateMetadata{BuildID: "build-1"},
	}, nil)

	cache := peerprovidermocks.NewMockCache(t)
	cache.EXPECT().GetCachedTemplate("build-1").Return(tmplMock, true)

	src, err := ResolveBlob(cache, "build-1", storage.MetadataName)
	require.NoError(t, err)

	sender := &collectSender{}

	require.NoError(t, src.Stream(t.Context(), sender))
	assert.Contains(t, string(sender.data), "build-1")
}
