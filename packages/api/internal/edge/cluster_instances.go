package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type ClusterInstance struct {
	NodeID string

	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string

	roles  []infogrpc.ServiceInfoRole
	status infogrpc.ServiceInfoStatus
	mutex  sync.RWMutex
	tracer trace.Tracer
}

const (
	instancesSyncInterval = 15 * time.Second
	instancesSyncTimeout  = 15 * time.Second
)

func (c *Cluster) startSync() {
	c.synchronization.Start(instancesSyncInterval, instancesSyncTimeout, true)
}

func (c *Cluster) syncInstance(ctx context.Context, instance *ClusterInstance) {
	grpc := c.GetGRPC(instance.ServiceInstanceID)

	// we are taking service info directly from the instance to avoid timing delays in service discovery
	reqCtx := metadata.NewOutgoingContext(ctx, grpc.Metadata)
	info, err := grpc.Client.Info.ServiceInfo(reqCtx, &emptypb.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		zap.L().Error("Failed to get instance info", zap.Error(err), l.WithClusterID(c.ID), l.WithClusterNodeID(instance.NodeID))
		return
	}

	instance.mutex.Lock()
	defer instance.mutex.Unlock()

	instance.status = info.ServiceStatus
	instance.roles = info.ServiceRoles
}

func (n *ClusterInstance) GetStatus() infogrpc.ServiceInfoStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return n.status
}

func (n *ClusterInstance) hasRole(r infogrpc.ServiceInfoRole) bool {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return slices.Contains(n.roles, r)
}

func (n *ClusterInstance) IsBuilder() bool {
	return n.hasRole(infogrpc.ServiceInfoRole_TemplateBuilder)
}

func (n *ClusterInstance) IsOrchestrator() bool {
	return n.hasRole(infogrpc.ServiceInfoRole_Orchestrator)
}

// SynchronizationStore defines methods for synchronizing cluster instances
type clusterSynchronizationStore struct {
	cluster *Cluster
}

func (d clusterSynchronizationStore) SourceList(ctx context.Context) ([]api.ClusterOrchestratorNode, error) {
	// fetch cluster instances with use of service discovery
	res, err := d.cluster.httpClient.V1ServiceDiscoveryGetOrchestratorsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster instances from service discovery: %w", err)
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request to get builders returned nil response")
	}

	return *res.JSON200, nil
}

func (d clusterSynchronizationStore) SourceExists(ctx context.Context, s []api.ClusterOrchestratorNode, p *ClusterInstance) bool {
	for _, item := range s {
		if item.NodeID == p.NodeID {
			return true
		}
	}

	return false
}

func (d clusterSynchronizationStore) PoolList(ctx context.Context) []*ClusterInstance {
	mapped := make([]*ClusterInstance, 0)
	for _, item := range d.cluster.instances.Items() {
		mapped = append(mapped, item)
	}

	return mapped
}

func (d clusterSynchronizationStore) PoolExists(ctx context.Context, s api.ClusterOrchestratorNode) bool {
	_, found := d.cluster.instances.Get(s.NodeID)
	return found
}

func (d clusterSynchronizationStore) PoolInsert(ctx context.Context, item api.ClusterOrchestratorNode) {
	zap.L().Info("Adding new instance into cluster pool", l.WithClusterID(d.cluster.ID), l.WithClusterNodeID(item.NodeID))

	instance := &ClusterInstance{
		NodeID: item.NodeID,

		ServiceInstanceID:    item.ServiceInstanceID,
		ServiceVersion:       item.ServiceVersion,
		ServiceVersionCommit: item.ServiceVersionCommit,

		// initial values before first sync
		status: infogrpc.ServiceInfoStatus_OrchestratorUnhealthy,
		roles:  make([]infogrpc.ServiceInfoRole, 0),

		tracer: d.cluster.tracer,
		mutex:  sync.RWMutex{},
	}

	d.cluster.instances.Insert(item.NodeID, instance)
}

func (d clusterSynchronizationStore) PoolUpdate(ctx context.Context, instance *ClusterInstance) {
	d.cluster.syncInstance(ctx, instance)
}

func (d clusterSynchronizationStore) PoolRemove(ctx context.Context, cluster *ClusterInstance) {
	zap.L().Info("Removing instance from cluster pool", l.WithClusterID(d.cluster.ID), l.WithClusterNodeID(cluster.NodeID))
	d.cluster.instances.Remove(cluster.NodeID)
}
