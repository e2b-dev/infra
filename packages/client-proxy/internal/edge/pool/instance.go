package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	e2bgrpcorchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type OrchestratorStatus string

const (
	instanceSyncMaxRetries = 3

	OrchestratorStatusHealthy   OrchestratorStatus = "healthy"
	OrchestratorStatusDraining  OrchestratorStatus = "draining"
	OrchestratorStatusUnhealthy OrchestratorStatus = "unhealthy"
)

type InstanceInfo struct {
	NodeID string

	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string
	ServiceStatus        OrchestratorStatus
	ServiceStartup       time.Time

	Host  string
	IP    string
	Roles []e2bgrpcorchestratorinfo.ServiceInfoRole
}

type Instance struct {
	metricVCpuUsed         atomic.Uint32
	metricMemoryUsedBytes  atomic.Uint64
	metricDiskUsedBytes    atomic.Uint64
	metricSandboxesRunning atomic.Uint32

	client *instanceGRPCClient
	info   InstanceInfo
	mutex  sync.RWMutex
}

func newInstance(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, ip string, port uint16) (*Instance, error) {
	host := fmt.Sprintf("%s:%d", ip, port)

	client, err := newClient(tracerProvider, meterProvider, host)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	o := &Instance{
		client: client,
		info: InstanceInfo{
			Host: host,
			IP:   ip,
		},
	}

	return o, nil
}

func (o *Instance) sync(ctx context.Context) error {
	for range instanceSyncMaxRetries {
		freshInfo := o.GetInfo()

		status, err := o.client.info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			logger.L().Error(ctx, "failed to check orchestrator health", logger.WithNodeID(freshInfo.NodeID), zap.Error(err))

			continue
		}

		freshInfo.NodeID = status.GetNodeId()
		freshInfo.ServiceInstanceID = status.GetServiceId()
		freshInfo.ServiceStartup = status.GetServiceStartup().AsTime()
		freshInfo.ServiceStatus = getMappedStatus(ctx, status.GetServiceStatus())
		freshInfo.ServiceVersion = status.GetServiceVersion()
		freshInfo.ServiceVersionCommit = status.GetServiceCommit()
		freshInfo.Roles = status.GetServiceRoles()
		o.setInfo(freshInfo)

		o.metricSandboxesRunning.Store(status.GetMetricSandboxesRunning())
		o.metricMemoryUsedBytes.Store(status.GetMetricMemoryUsedBytes())
		o.metricDiskUsedBytes.Store(status.GetMetricDiskAllocatedBytes())
		o.metricVCpuUsed.Store(status.GetMetricCpuCount())

		return nil
	}

	return errors.New("failed to check orchestrator status")
}

func (o *Instance) setStatus(status OrchestratorStatus) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.info.ServiceStatus = status
}

func (o *Instance) setInfo(i InstanceInfo) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.info = i
}

func (o *Instance) GetInfo() InstanceInfo {
	o.mutex.RLock()
	defer o.mutex.RUnlock()

	return o.info
}

func (o *Instance) GetClient() e2bgrpcorchestratorinfo.InfoServiceClient {
	return o.client.info
}

func (o *Instance) GetConnection() *grpc.ClientConn {
	return o.client.connection
}

func (o *Instance) Close() error {
	// close sync context
	o.setStatus(OrchestratorStatusUnhealthy)

	// close grpc client
	if o.client != nil {
		err := o.client.close()
		if err != nil {
			return err
		}
	}

	return nil
}

func getMappedStatus(ctx context.Context, s e2bgrpcorchestratorinfo.ServiceInfoStatus) OrchestratorStatus {
	switch s {
	case e2bgrpcorchestratorinfo.ServiceInfoStatus_Healthy:
		return OrchestratorStatusHealthy
	case e2bgrpcorchestratorinfo.ServiceInfoStatus_Draining:
		return OrchestratorStatusDraining
	case e2bgrpcorchestratorinfo.ServiceInfoStatus_Unhealthy:
		return OrchestratorStatusUnhealthy
	}

	logger.L().Error(ctx, "Unknown service info status", zap.String("status", s.String()))

	return OrchestratorStatusUnhealthy
}
