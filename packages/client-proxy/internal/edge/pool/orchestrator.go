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
	Ip    string
	Roles []e2bgrpcorchestratorinfo.ServiceInfoRole
}

type OrchestratorInstance struct {
	MetricVCpuUsed         atomic.Int64
	MetricMemoryUsedInMB   atomic.Int64
	MetricDiskUsedInMB     atomic.Int64
	MetricSandboxesRunning atomic.Int64

	client *OrchestratorGRPCClient
	info   OrchestratorInstanceInfo
	mutex  sync.RWMutex
}

type OrchestratorGRPCClient struct {
	Info       e2bgrpcorchestratorinfo.InfoServiceClient
	Connection *grpc.ClientConn
}

func NewOrchestratorInstance(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, ip string, port int) (*OrchestratorInstance, error) {
	host := fmt.Sprintf("%s:%d", ip, port)

	client, err := newClient(tracerProvider, meterProvider, host)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	o := &OrchestratorInstance{
		client: client,
		info: OrchestratorInstanceInfo{
			Host: host,
			Ip:   ip,
		},
	}

	return o, nil
}

func (o *OrchestratorInstance) sync(ctx context.Context) error {
	for i := 0; i < orchestratorSyncMaxRetries; i++ {
		freshInfo := o.GetInfo()

		status, err := o.client.Info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			zap.L().Error("failed to check orchestrator health", l.WithClusterNodeID(freshInfo.NodeID), zap.Error(err))
			continue
		}

		freshInfo.NodeID = status.NodeId
		freshInfo.ServiceInstanceID = status.ServiceId
		freshInfo.ServiceStartup = status.ServiceStartup.AsTime()
		freshInfo.ServiceStatus = getMappedStatus(status.ServiceStatus)
		freshInfo.ServiceVersion = status.ServiceVersion
		freshInfo.ServiceVersionCommit = status.ServiceCommit
		freshInfo.Roles = status.ServiceRoles

		freshInfo.ServiceVersion = status.ServiceVersion
		freshInfo.ServiceVersionCommit = status.ServiceCommit
		o.setInfo(freshInfo)

		o.MetricSandboxesRunning.Store(status.MetricSandboxesRunning)
		o.MetricMemoryUsedInMB.Store(status.MetricMemoryUsedMb)
		o.MetricDiskUsedInMB.Store(status.MetricDiskMb)
		o.MetricVCpuUsed.Store(status.MetricVcpuUsed)

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

func getMappedStatus(s e2bgrpcorchestratorinfo.ServiceInfoStatus) OrchestratorStatus {
	switch s {
	case e2bgrpcorchestratorinfo.ServiceInfoStatus_OrchestratorHealthy:
		return OrchestratorStatusHealthy
	case e2bgrpcorchestratorinfo.ServiceInfoStatus_OrchestratorDraining:
		return OrchestratorStatusDraining
	case e2bgrpcorchestratorinfo.ServiceInfoStatus_OrchestratorUnhealthy:
		return OrchestratorStatusUnhealthy
	}

	zap.L().Error("Unknown service info status", zap.Any("status", s))
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
