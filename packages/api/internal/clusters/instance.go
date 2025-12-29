package clusters

import (
	"context"
	"fmt"
	"slices"
	"sync"

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

type Instance struct {
	// Identifier that uniquely identifies the instance so it will not be registered multiple times.
	// Depending on service discovery used, it can be combination of different parameters, what service discovery gives us.
	uniqueIdentifier string

	ClusterID uuid.UUID
	NodeID    string

	serviceInstanceID    string
	serviceVersion       string
	serviceVersionCommit string

	roles       []infogrpc.ServiceInfoRole
	machineInfo machineinfo.MachineInfo
	grpc        *GRPCClient

	status infogrpc.ServiceInfoStatus
	mutex  sync.RWMutex
}

// InstanceInfo contains synchronized instance information
type InstanceInfo struct {
	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string
	Status               infogrpc.ServiceInfoStatus
	Roles                []infogrpc.ServiceInfoRole
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
	conn, err := createConnection(tel, clusterAuth, connAddr, connTls)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster instance grpc client: %w", err)
	}

	// Create with default values that will be updated on sync before returning the instance,
	// so we will never have uninitialized instance status or roles.
	//
	// For case with local cluster we will not receive instance ID from service discovery, but its not needed for proxy routing,
	// so it can be empty and will be filled after first sync.
	i := &Instance{
		uniqueIdentifier:  sd.UniqueIdentifier,
		NodeID:            sd.NodeID,
		ClusterID:         clusterID,
		serviceInstanceID: sd.InstanceID,

		grpc:  conn,
		mutex: sync.RWMutex{},
	}

	err = i.Sync(ctx)
	if err != nil {
		closeErr := conn.Close()
		if closeErr != nil {
			logger.L().Error(
				ctx, "Failed to close gRPC Connection after instance sync failure",
				zap.Error(err),
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
	info, err := i.grpc.Info.ServiceInfo(ctx, &emptypb.Empty{})
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return err
	}

	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.status = info.GetServiceStatus()
	i.roles = info.GetServiceRoles()
	i.machineInfo = machineinfo.FromGRPCInfo(info.GetMachineInfo())

	i.serviceInstanceID = info.GetServiceId()
	i.serviceVersion = info.GetServiceVersion()
	i.serviceVersionCommit = info.GetServiceCommit()

	return nil
}

func (i *Instance) GetMachineInfo() machineinfo.MachineInfo {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.machineInfo
}

func (i *Instance) GetInfo() InstanceInfo {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return InstanceInfo{
		ServiceInstanceID:    i.serviceInstanceID,
		ServiceVersion:       i.serviceVersion,
		ServiceVersionCommit: i.serviceVersionCommit,
		Status:               i.status,
		Roles:                slices.Clone(i.roles),
	}
}

func (i *Instance) hasRole(r infogrpc.ServiceInfoRole) bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return slices.Contains(i.roles, r)
}

func (i *Instance) IsBuilder() bool {
	return i.hasRole(infogrpc.ServiceInfoRole_TemplateBuilder)
}

func (i *Instance) IsOrchestrator() bool {
	return i.hasRole(infogrpc.ServiceInfoRole_Orchestrator)
}

func (i *Instance) GetConnection() *GRPCClient {
	return i.grpc
}

func (i *Instance) Close() error {
	return i.grpc.Close()
}
