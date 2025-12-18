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
	UniqueIdentifier string

	NodeID    string
	ClusterID uuid.UUID

	// Following fields are available only for local cluster.
	// For remote clusters gRPC proxy is used and these fields are not needed.
	LocalIPAddress string
	LocalProxyPort uint16

	InstanceID           string
	ServiceVersion       string
	ServiceVersionCommit string

	grpc *GRPCClient

	roles       []infogrpc.ServiceInfoRole
	machineInfo machineinfo.MachineInfo
	status      infogrpc.ServiceInfoStatus
	mutex       sync.RWMutex
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
		UniqueIdentifier: sd.UniqueIdentifier,
		NodeID:           sd.NodeID,
		ClusterID:        clusterID,
		InstanceID:       sd.InstanceID,

		LocalIPAddress: sd.LocalIPAddress,
		LocalProxyPort: sd.LocalProxyPort,

		grpc:  conn,
		mutex: sync.RWMutex{},
	}

	err = i.Sync(ctx)
	if err != nil {
		closeErr := conn.Close()
		if closeErr != nil {
			logger.L().Error(
				ctx, "Failed to close gRPC connection after instance sync failure",
				zap.Error(err),
				logger.WithNodeID(i.NodeID),
				logger.WithClusterID(i.ClusterID),
				logger.WithServiceInstanceID(i.InstanceID),
			)
		}

		return nil, err
	}

	return i, nil
}

// Sync function can be called on freshly initialized instance to populate its data
// In initial case its possible that service instance id needed for proper remote cluster routing is not yet set.
func (i *Instance) Sync(ctx context.Context) error {
	info, err := i.GetConnection().Info.ServiceInfo(ctx, &emptypb.Empty{})
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return err
	}

	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.status = info.GetServiceStatus()
	i.roles = info.GetServiceRoles()
	i.machineInfo = machineinfo.FromGRPCInfo(info.GetMachineInfo())

	i.InstanceID = info.GetServiceId()
	i.ServiceVersion = info.GetServiceVersion()
	i.ServiceVersionCommit = info.GetServiceCommit()

	return nil
}

func (i *Instance) GetStatus() infogrpc.ServiceInfoStatus {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.status
}

func (i *Instance) GetMachineInfo() machineinfo.MachineInfo {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.machineInfo
}

func (i *Instance) GetConnection() *GRPCClient {
	return i.grpc
}

func (i *Instance) IsBuilder() bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return slices.Contains(i.roles, infogrpc.ServiceInfoRole_TemplateBuilder)
}

func (i *Instance) IsOrchestrator() bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return slices.Contains(i.roles, infogrpc.ServiceInfoRole_Orchestrator)
}

func (i *Instance) Close() error {
	return i.grpc.Close()
}
