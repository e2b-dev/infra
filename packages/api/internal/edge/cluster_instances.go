package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

type ClusterInstance struct {
	NodeID string

	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string

	roles       []infogrpc.ServiceInfoRole
	machineInfo machineinfo.MachineInfo

	status infogrpc.ServiceInfoStatus
	mutex  sync.RWMutex
}

const (
	instancesSyncInterval = 5 * time.Second
	instancesSyncTimeout  = 5 * time.Second
)

func (c *Cluster) startSync(ctx context.Context) {
	c.synchronization.Start(ctx, instancesSyncInterval, instancesSyncTimeout, true)
}

func (c *Cluster) syncInstance(ctx context.Context, instance *ClusterInstance) {
	grpc := c.GetGRPC(instance.ServiceInstanceID)

	// we are taking service info directly from the instance to avoid timing delays in service discovery
	reqCtx := metadata.NewOutgoingContext(ctx, grpc.Metadata)
	info, err := grpc.Client.Info.ServiceInfo(reqCtx, &emptypb.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		logger.L().Error(ctx, "Failed to get instance info",
			zap.Error(err),
			logger.WithClusterID(c.ID),
			logger.WithNodeID(instance.NodeID),
			logger.WithServiceInstanceID(instance.ServiceInstanceID),
		)

		return
	}

	instance.mutex.Lock()
	defer instance.mutex.Unlock()

	instance.status = info.GetServiceStatus()
	instance.roles = info.GetServiceRoles()
	instance.machineInfo = machineinfo.FromGRPCInfo(info.GetMachineInfo())

}

func (n *ClusterInstance) GetStatus() infogrpc.ServiceInfoStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.status
}

func (n *ClusterInstance) GetMachineInfo() machineinfo.MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
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

func (d clusterSynchronizationStore) SourceExists(_ context.Context, s []api.ClusterOrchestratorNode, p *ClusterInstance) bool {
	for _, item := range s {
		// With comparing service instance ID we ensure when orchestrator on same node and node ID is still same
		// we will properly clean up old instance and later register as new one
		if item.ServiceInstanceID == p.ServiceInstanceID {
			return true
		}
	}

	return false
}

func (d clusterSynchronizationStore) PoolList(_ context.Context) []*ClusterInstance {
	clusterInstanceItems := d.cluster.instances.Items()
	mapped := make([]*ClusterInstance, 0, len(clusterInstanceItems))
	for _, item := range clusterInstanceItems {
		mapped = append(mapped, item)
	}

	return mapped
}

func (d clusterSynchronizationStore) PoolExists(_ context.Context, s api.ClusterOrchestratorNode) bool {
	_, found := d.cluster.instances.Get(s.NodeID)

	return found
}

func (d clusterSynchronizationStore) PoolInsert(ctx context.Context, item api.ClusterOrchestratorNode) {
	logger.L().Info(ctx, "Adding instance into cluster pool",
		logger.WithClusterID(d.cluster.ID),
		logger.WithNodeID(item.NodeID),
		logger.WithServiceInstanceID(item.ServiceInstanceID),
	)

	instance := &ClusterInstance{
		NodeID: item.NodeID,

		ServiceInstanceID:    item.ServiceInstanceID,
		ServiceVersion:       item.ServiceVersion,
		ServiceVersionCommit: item.ServiceVersionCommit,

		// initial values before first sync
		status: infogrpc.ServiceInfoStatus_Unhealthy,
		roles:  make([]infogrpc.ServiceInfoRole, 0),

		mutex: sync.RWMutex{},
	}

	d.cluster.syncInstance(ctx, instance)
	d.cluster.instances.Insert(item.NodeID, instance)
}

func (d clusterSynchronizationStore) PoolUpdate(ctx context.Context, instance *ClusterInstance) {
	d.cluster.syncInstance(ctx, instance)
}

func (d clusterSynchronizationStore) PoolRemove(ctx context.Context, instance *ClusterInstance) {
	logger.L().Info(ctx, "Removing instance from cluster pool",
		logger.WithClusterID(d.cluster.ID),
		logger.WithNodeID(instance.NodeID),
		logger.WithServiceInstanceID(instance.ServiceInstanceID),
	)
	d.cluster.instances.Remove(instance.NodeID)
}
