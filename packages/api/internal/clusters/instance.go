package clusters

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/clusters/discovery"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// maxSyncFailuresBeforeUnhealthy defines the number of consecutive sync failures
	// before an instance is marked as unhealthy.
	maxSyncFailuresBeforeUnhealthy = 3

	maxInstanceSyncCallTimeout = 1 * time.Second
)

type Instance struct {
	// Identifier that uniquely identifies the instance so it will not be registered multiple times.
	// Depending on service discovery used, it can be combination of different parameters, what service discovery gives us.
	uniqueIdentifier string

	ClusterID uuid.UUID
	NodeID    string

	serviceInstanceID    string
	serviceVersion       string
	serviceVersionCommit string

	client         *GRPCClient
	status         infogrpc.ServiceInfoStatus
	machine        machineinfo.MachineInfo
	roles          []infogrpc.ServiceInfoRole
	isBuilder      bool
	isOrchestrator bool

	syncFailCount int

	mutex sync.RWMutex
}

// InstanceInfo contains synchronized instance information
type InstanceInfo struct {
	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string
	Status               infogrpc.ServiceInfoStatus
	IsOrchestrator       bool
	IsBuilder            bool
}

func newInstance(
	ctx context.Context,
	tel *telemetry.Client,
	clusterAuth *instanceAuthorization,
	clusterID uuid.UUID,
	sd discovery.Item,
	connAddr string,
	connTls bool,
) (*Instance, error) {
	client, err := createClient(tel, clusterAuth, connAddr, connTls)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster instance client client: %w", err)
	}

	// Create with default values that will be updated on sync before returning the instance,
	// so we will never have uninitialized instance status or roles.
	//
	// For case with local cluster we will not receive instance ID from service discovery, but its not needed for proxy routing,
	// so it can be empty and will be filled after first sync.
	i := &Instance{
		uniqueIdentifier:  sd.UniqueIdentifier,
		serviceInstanceID: sd.InstanceID,
		NodeID:            sd.NodeID,
		ClusterID:         clusterID,

		client: client,
		mutex:  sync.RWMutex{},
	}

	err = i.Sync(ctx)
	if err != nil {
		closeErr := client.Close()
		if closeErr != nil {
			logger.L().Error(
				ctx, "Failed to close gRPC Connection after instance sync failure",
				zap.Error(closeErr),
				logger.WithNodeID(i.NodeID),
				logger.WithClusterID(i.ClusterID),
				logger.WithServiceInstanceID(i.serviceInstanceID),
			)
		}

		return nil, err
	}

	return i, nil
}

// Sync function can be called on freshly initialized instance to populate its data
// In initial case its possible that service instance id needed for proper remote cluster routing is not yet set.
func (i *Instance) Sync(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, maxInstanceSyncCallTimeout)
	defer cancel()

	info, err := i.client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		i.mutex.Lock()
		defer i.mutex.Unlock()

		// Increase fail count and set unhealthy status if needed
		i.syncFailCount++
		if i.syncFailCount >= maxSyncFailuresBeforeUnhealthy {
			logger.L().Warn(ctx, "Instance sync failed multiple times, marking instance as unhealthy",
				logger.WithNodeID(i.NodeID),
				logger.WithClusterID(i.ClusterID),
				logger.WithServiceInstanceID(i.serviceInstanceID),
				zap.Int("counter", i.syncFailCount),
				zap.Error(err),
			)

			i.status = infogrpc.ServiceInfoStatus_Unhealthy
		}

		return err
	}

	i.mutex.Lock()
	defer i.mutex.Unlock()

	// Reset fail count on successful sync
	i.syncFailCount = 0

	i.status = info.GetServiceStatus()
	i.roles = info.GetServiceRoles()
	i.machine = machineinfo.FromGRPCInfo(info.GetMachineInfo())

	i.serviceInstanceID = info.GetServiceId()
	i.serviceVersion = info.GetServiceVersion()
	i.serviceVersionCommit = info.GetServiceCommit()

	// We don't want to check array every time,
	// this is why we are caching value during instance sync.
	i.isBuilder = slices.Contains(i.roles, infogrpc.ServiceInfoRole_TemplateBuilder)
	i.isOrchestrator = slices.Contains(i.roles, infogrpc.ServiceInfoRole_Orchestrator)

	return nil
}

func (i *Instance) GetMachineInfo() machineinfo.MachineInfo {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.machine
}

func (i *Instance) GetInfo() InstanceInfo {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return InstanceInfo{
		ServiceInstanceID:    i.serviceInstanceID,
		ServiceVersion:       i.serviceVersion,
		ServiceVersionCommit: i.serviceVersionCommit,
		Status:               i.status,
		IsOrchestrator:       i.isOrchestrator,
		IsBuilder:            i.isBuilder,
	}
}

func (i *Instance) GetClient() *GRPCClient {
	return i.client
}

func (i *Instance) Close() error {
	return i.client.Close()
}
