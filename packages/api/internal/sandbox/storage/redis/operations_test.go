package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type failingRoutingCatalog struct {
	deleteCalled bool
}

func (c *failingRoutingCatalog) GetSandbox(context.Context, string) (*e2bcatalog.SandboxInfo, error) {
	return nil, e2bcatalog.ErrSandboxNotFound
}

func (c *failingRoutingCatalog) StoreSandbox(context.Context, string, *e2bcatalog.SandboxInfo, time.Duration) error {
	return errors.New("routing unavailable")
}

func (c *failingRoutingCatalog) DeleteSandbox(context.Context, string, string) error {
	c.deleteCalled = true

	return nil
}

func (c *failingRoutingCatalog) Close(context.Context) error {
	return nil
}

func TestAddReturnsRoutingCatalogError(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	storage := newTestStorage(t, client)
	catalog := &failingRoutingCatalog{}
	storage.routingCatalog = catalog

	now := time.Now()
	sbx := makeIndexedSandbox(uuid.New(), "sandbox", uuid.NewString(), now, now.Add(time.Hour))
	routing := &sandboxtypes.RoutingMetadata{
		OrchestratorID: "orchestrator",
		OrchestratorIP: "10.0.0.1",
	}

	err := storage.Add(t.Context(), sbx, routing)
	require.ErrorContains(t, err, "failed to add sandbox to routing catalog")
	require.True(t, catalog.deleteCalled)
	_, err = storage.Get(t.Context(), sbx.TeamID, sbx.SandboxID)
	require.ErrorIs(t, err, sandboxtypes.ErrNotFound)
	requireMemberAbsent(t, client, sandboxExpirationMember(sbx))
}

func TestRemoveDeletesRoutingCatalogEntry(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	storage := newTestStorage(t, client)
	now := time.Now()
	sbx := makeIndexedSandbox(uuid.New(), "sandbox", uuid.NewString(), now, now.Add(time.Hour))
	routing := &sandboxtypes.RoutingMetadata{
		OrchestratorID: "orchestrator",
		OrchestratorIP: "10.0.0.1",
	}
	require.NoError(t, storage.Add(t.Context(), sbx, routing))
	_, err := storage.routingCatalog.GetSandbox(t.Context(), sbx.SandboxID)
	require.NoError(t, err)

	require.NoError(t, storage.Remove(t.Context(), sbx.TeamID, sbx.SandboxID))
	_, err = storage.routingCatalog.GetSandbox(t.Context(), sbx.SandboxID)
	require.ErrorIs(t, err, e2bcatalog.ErrSandboxNotFound)
}
