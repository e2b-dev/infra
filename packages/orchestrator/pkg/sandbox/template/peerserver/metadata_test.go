package peerserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	templatemocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/mocks"
	peerservermocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerserver/mocks"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestMetadataSource_Stream(t *testing.T) {
	t.Parallel()

	tmplMock := templatemocks.NewMockTemplate(t)
	tmplMock.EXPECT().Metadata().Return(metadata.Template{
		Template: metadata.TemplateMetadata{BuildID: "build-1"},
	}, nil)

	cache := peerservermocks.NewMockCache(t)
	cache.EXPECT().GetCachedTemplate("build-1").Return(tmplMock, true)

	src, err := ResolveBlob(cache, "build-1", storage.MetadataName)
	require.NoError(t, err)

	sender := &collectSender{}

	require.NoError(t, src.Stream(t.Context(), sender))
	assert.Contains(t, string(sender.data), "build-1")
}
