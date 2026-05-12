package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	sandboxmemory "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	orchgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandboxroutingcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type recordingUpdateSandboxClient struct {
	orchgrpc.SandboxServiceClient

	mu      sync.Mutex
	updates []*orchgrpc.SandboxUpdateRequest
}

func (c *recordingUpdateSandboxClient) Update(_ context.Context, req *orchgrpc.SandboxUpdateRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.updates = append(c.updates, req)

	return &emptypb.Empty{}, nil
}

func (c *recordingUpdateSandboxClient) updateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.updates)
}

func (c *recordingUpdateSandboxClient) lastUpdate() *orchgrpc.SandboxUpdateRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.updates) == 0 {
		return nil
	}

	return c.updates[len(c.updates)-1]
}

func newKeepAliveTestOrchestrator(t *testing.T) (*Orchestrator, *recordingUpdateSandboxClient, sandboxroutingcatalog.SandboxesCatalog, *nodemanager.Node) {
	t.Helper()

	store := sandbox.NewStore(
		sandboxmemory.NewStorage(),
		reservations.NewReservationStorage(),
		sandbox.Callbacks{
			AddSandboxToRoutingTable: func(context.Context, sandbox.Sandbox) {},
			AsyncNewlyCreatedSandbox: func(context.Context, sandbox.Sandbox, sandbox.CreationMetadata) {},
		},
	)

	catalog := sandboxroutingcatalog.NewMemorySandboxesCatalog()
	client := &recordingUpdateSandboxClient{}
	node := nodemanager.NewTestNode("node-1", api.NodeStatusReady, 0, 8)
	node.SetSandboxClient(client)

	o := &Orchestrator{
		sandboxStore:   store,
		nodes:          smap.New[*nodemanager.Node](),
		routingCatalog: catalog,
	}
	o.registerNode(node)

	return o, client, catalog, node
}

func keepAliveTestSandbox(teamID uuid.UUID, node *nodemanager.Node, now time.Time) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID:         "sbx-keepalive-" + uuid.New().String()[:8],
		ExecutionID:       "exec-" + uuid.New().String()[:8],
		TeamID:            teamID,
		StartTime:         now.Add(-time.Minute),
		EndTime:           now.Add(time.Minute),
		MaxInstanceLength: time.Hour,
		State:             sandbox.StateRunning,
		NodeID:            node.ID,
		ClusterID:         node.ClusterID,
		Lifecycle: types.SandboxLifecycleConfig{
			Keepalive: &types.SandboxKeepaliveConfig{
				Traffic: &types.SandboxTrafficKeepaliveConfig{Timeout: 300},
			},
		},
	}
}

func TestGetMaxTTLNormal(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ttl := getMaxAllowedTTL(now, now, 2*time.Hour, 3*time.Hour)
	assert.Equal(t, 2*time.Hour, ttl)
}

func TestGetMaxTTLMax(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ttl := getMaxAllowedTTL(now, now, 4*time.Hour, 3*time.Hour)
	assert.Equal(t, 3*time.Hour, ttl)
}

func TestGetMaxTTLExpired(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ttl := getMaxAllowedTTL(now, now.Add(-2*time.Hour), 4*time.Hour, time.Hour)
	assert.Equal(t, time.Duration(0), ttl)
}

func TestKeepAliveFor_ExtendsSandboxAndRefreshesRoutingCatalog(t *testing.T) {
	t.Parallel()

	o, client, catalog, node := newKeepAliveTestOrchestrator(t)
	teamID := uuid.New()
	now := time.Now()
	sbx := keepAliveTestSandbox(teamID, node, now)
	require.NoError(t, o.sandboxStore.Add(t.Context(), sbx, nil))

	updated, apiErr := o.KeepAliveFor(t.Context(), teamID, sbx.SandboxID, 5*time.Minute, false)
	require.Nil(t, apiErr)
	require.NotNil(t, updated)
	assert.True(t, updated.EndTime.After(sbx.EndTime))

	require.Equal(t, 1, client.updateCount())
	require.NotNil(t, client.lastUpdate())
	assert.Equal(t, sbx.SandboxID, client.lastUpdate().GetSandboxId())
	assert.True(t, client.lastUpdate().GetEndTime().AsTime().Equal(updated.EndTime))

	routingInfo, err := catalog.GetSandbox(t.Context(), sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, teamID.String(), routingInfo.TeamID)
	assert.Equal(t, sbx.ExecutionID, routingInfo.ExecutionID)
	assert.True(t, routingInfo.TrafficKeepalive)
	assert.True(t, routingInfo.StartedAt.Equal(sbx.StartTime))
	assert.Equal(t, int64(1), routingInfo.MaxLengthInHours)
}

func TestKeepAliveFor_ShorterNoopSkipsNodeAndRoutingUpdates(t *testing.T) {
	t.Parallel()

	o, client, catalog, node := newKeepAliveTestOrchestrator(t)
	teamID := uuid.New()
	now := time.Now()
	sbx := keepAliveTestSandbox(teamID, node, now)
	sbx.EndTime = now.Add(30 * time.Minute)
	require.NoError(t, o.sandboxStore.Add(t.Context(), sbx, nil))

	updated, apiErr := o.KeepAliveFor(t.Context(), teamID, sbx.SandboxID, time.Minute, false)
	require.Nil(t, apiErr)
	require.NotNil(t, updated)
	assert.True(t, updated.EndTime.Equal(sbx.EndTime))
	assert.Equal(t, 0, client.updateCount())

	_, err := catalog.GetSandbox(t.Context(), sbx.SandboxID)
	assert.ErrorIs(t, err, sandboxroutingcatalog.ErrSandboxNotFound)
}
