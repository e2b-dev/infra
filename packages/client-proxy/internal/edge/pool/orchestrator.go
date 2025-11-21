package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	e2bgrpcorchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type OrchestratorStatus string

const (
	orchestratorSyncMaxRetries = 3

	OrchestratorStatusHealthy   OrchestratorStatus = "healthy"
	OrchestratorStatusDraining  OrchestratorStatus = "draining"
	OrchestratorStatusUnhealthy OrchestratorStatus = "unhealthy"
)

type OrchestratorInstanceInfo struct {
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

type OrchestratorInstance struct {
	MetricVCpuUsed         atomic.Uint32
	MetricMemoryUsedBytes  atomic.Uint64
	MetricDiskUsedBytes    atomic.Uint64
	MetricSandboxesRunning atomic.Uint32

	client *OrchestratorGRPCClient
	info   OrchestratorInstanceInfo
	mutex  sync.RWMutex
}

type OrchestratorGRPCClient struct {
	Info       e2bgrpcorchestratorinfo.InfoServiceClient
	Connection *grpc.ClientConn
}

func NewOrchestratorInstance(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, ip string, port uint16) (*OrchestratorInstance, error) {
	host := fmt.Sprintf("%s:%d", ip, port)

	client, err := newClient(tracerProvider, meterProvider, host)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	o := &OrchestratorInstance{
		client: client,
		info: OrchestratorInstanceInfo{
			Host: host,
			IP:   ip,
		},
	}

	return o, nil
}

func (o *OrchestratorInstance) sync(ctx context.Context) error {
	for range orchestratorSyncMaxRetries {
		freshInfo := o.GetInfo()

		status, err := o.client.Info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			logger.L().Error(ctx, "failed to check orchestrator health", l.WithNodeID(freshInfo.NodeID), zap.Error(err))

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

		o.MetricSandboxesRunning.Store(status.GetMetricSandboxesRunning())
		o.MetricMemoryUsedBytes.Store(status.GetMetricMemoryUsedBytes())
		o.MetricDiskUsedBytes.Store(status.GetMetricDiskAllocatedBytes())
		o.MetricVCpuUsed.Store(status.GetMetricCpuCount())

		return nil
	}

	return errors.New("failed to check orchestrator status")
}

func (o *OrchestratorInstance) setStatus(status OrchestratorStatus) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.info.ServiceStatus = status
}

func (o *OrchestratorInstance) setInfo(i OrchestratorInstanceInfo) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.info = i
}

func (o *OrchestratorInstance) GetInfo() OrchestratorInstanceInfo {
	o.mutex.RLock()
	defer o.mutex.RUnlock()

	return o.info
}

func (o *OrchestratorInstance) GetClient() *OrchestratorGRPCClient {
	return o.client
}

func (o *OrchestratorInstance) Close() error {
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

func newClient(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, host string) (*OrchestratorGRPCClient, error) {
	conn, err := grpc.NewClient(host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithMeterProvider(meterProvider),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	return &OrchestratorGRPCClient{
		Info:       e2bgrpcorchestratorinfo.NewInfoServiceClient(conn),
		Connection: conn,
	}, nil
}

func (a *OrchestratorGRPCClient) close() error {
	err := a.Connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
