package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type stubResumer struct {
	nodeIP string
	err    error
}

func (s stubResumer) Resume(_ context.Context, _ string) (string, error) {
	return s.nodeIP, s.err
}

func newFF(t *testing.T, autoResumeEnabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.SandboxAutoResumeFlag.Key()).VariationForAll(autoResumeEnabled))

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff
}

func TestCatalogResolution_CatalogHit(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	err := c.StoreSandbox(context.Background(), "sbx", &catalog.SandboxInfo{
		OrchestratorIP: "10.0.0.1",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(context.Background(), "sbx", c, stubResumer{}, ff)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)
}

func TestCatalogResolution_CatalogHit_EmptyIPReturnsEmpty(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	err := c.StoreSandbox(context.Background(), "sbx", &catalog.SandboxInfo{
		OrchestratorIP: "",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(context.Background(), "sbx", c, stubResumer{}, ff)
	require.NoError(t, err)
	require.Empty(t, nodeIP)
}

func TestCatalogResolution_CatalogMiss_NoResumer(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	_, err := catalogResolution(context.Background(), "sbx", c, nil, ff)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestCatalogResolution_CatalogMiss_AutoResumeNotAllowed_FlagDisabled(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, false)

	_, err := catalogResolution(context.Background(), "sbx", c, stubResumer{nodeIP: "10.0.0.1"}, ff)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestCatalogResolution_CatalogMiss_AutoResumeNotAllowed_NotFound(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	_, err := catalogResolution(context.Background(), "sbx", c, stubResumer{err: status.Error(codes.NotFound, "not allowed")}, ff)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestCatalogResolution_CatalogMiss_AutoResumeErrored(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	_, err := catalogResolution(context.Background(), "sbx", c, stubResumer{err: status.Error(codes.Unavailable, "boom")}, ff)
	require.Error(t, err)
}

func TestCatalogResolution_CatalogMiss_AutoResumeSucceeded(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	nodeIP, err := catalogResolution(context.Background(), "sbx", c, stubResumer{nodeIP: "10.0.0.1"}, ff)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)
}

func TestCatalogResolution_CatalogMiss_AutoResumeSucceeded_EmptyIPReturnsEmpty(t *testing.T) {
	t.Parallel()

	c := catalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	nodeIP, err := catalogResolution(context.Background(), "sbx", c, stubResumer{nodeIP: ""}, ff)
	require.NoError(t, err)
	require.Empty(t, nodeIP)
}
