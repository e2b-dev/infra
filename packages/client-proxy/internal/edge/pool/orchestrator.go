package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	e2bgrpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	e2bgrpcorchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	e2bgrpctemplatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type OrchestratorStatus string

const (
	orchestratorSyncInterval   = 10 * time.Second
	orchestratorSyncMaxRetries = 3

	OrchestratorStatusHealthy   OrchestratorStatus = "healthy"
	OrchestratorStatusDraining  OrchestratorStatus = "draining"
	OrchestratorStatusUnhealthy OrchestratorStatus = "unhealthy"
)

type OrchestratorNode struct {
	ServiceId string
	NodeId    string

	SourceVersion string
	SourceCommit  string

	Host string
	Ip   string

	Status  OrchestratorStatus
	Startup time.Time
	Roles   []e2bgrpcorchestratorinfo.ServiceInfoRole

	Client *OrchestratorGRPCClient

	MetricVCpuUsed         atomic.Int64
	MetricMemoryUsedInMB   atomic.Int64
	MetricDiskUsedInMB     atomic.Int64
	MetricSandboxesRunning atomic.Int64

	mutex sync.Mutex

	ctx       context.Context
	ctxCancel context.CancelFunc
}

type OrchestratorGRPCClient struct {
	Sandbox  e2bgrpcorchestrator.SandboxServiceClient
	Template e2bgrpctemplatemanager.TemplateServiceClient
	Info     e2bgrpcorchestratorinfo.InfoServiceClient

	Connection *grpc.ClientConn
}

func NewOrchestrator(ctx context.Context, ip string, port int) (*OrchestratorNode, error) {
	host := fmt.Sprintf("%s:%d", ip, port)

	client, err := newClient(host)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	ctx, ctxCancel := context.WithCancel(ctx)

	o := &OrchestratorNode{
		Host:   host,
		Ip:     ip,
		Client: client,

		ctx:       ctx,
		ctxCancel: ctxCancel,
	}

	// run the first sync immediately
	err = o.syncRun()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize orchestrator, maybe its not ready yet: %w", err)
	}

	// initialize background sync to update orchestrator running sandboxes
	go func() { o.sync() }()

	return o, nil
}

func (o *OrchestratorNode) sync() {
	ticker := time.NewTicker(orchestratorSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			zap.L().Info("context done", zap.String("orchestrator sync id", o.ServiceId))
			return
		case <-ticker.C:
			o.syncRun()
		}
	}
}

func (o *OrchestratorNode) syncRun() error {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	ctx, cancel := context.WithTimeout(o.ctx, orchestratorSyncInterval)
	defer cancel()

	for i := 0; i < orchestratorSyncMaxRetries; i++ {
		status, err := o.Client.Info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			zap.L().Error("failed to check health", zap.String("orchestrator id", o.ServiceId), zap.Error(err))
			continue
		}

		o.NodeId = status.NodeId
		o.ServiceId = status.ServiceId
		o.Startup = status.ServiceStartup.AsTime()
		o.Status = getMappedStatus(status.ServiceStatus)
		o.Roles = status.ServiceRoles

		o.SourceVersion = status.ServiceVersion
		o.SourceCommit = status.ServiceCommit

		o.MetricSandboxesRunning.Store(status.MetricSandboxesRunning)
		o.MetricMemoryUsedInMB.Store(status.MetricMemoryUsedMb)
		o.MetricDiskUsedInMB.Store(status.MetricDiskMb)
		o.MetricVCpuUsed.Store(status.MetricVcpuUsed)

		return nil
	}

	return errors.New("failed to check orchestrator status")
}

func (o *OrchestratorNode) Close() error {
	// close sync context
	o.ctxCancel()
	o.Status = OrchestratorStatusUnhealthy

	// close grpc client
	if o.Client != nil {
		err := o.Client.close()
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

func newClient(host string) (*OrchestratorGRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), grpc.WithBlock(), grpc.WithTimeout(time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC Connection: %w", err)
	}

	return &OrchestratorGRPCClient{
		Sandbox:    e2bgrpcorchestrator.NewSandboxServiceClient(conn),
		Template:   e2bgrpctemplatemanager.NewTemplateServiceClient(conn),
		Info:       e2bgrpcorchestratorinfo.NewInfoServiceClient(conn),
		Connection: conn,
	}, nil
}

func (a *OrchestratorGRPCClient) close() error {
	err := a.Connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close Connection: %w", err)
	}

	return nil
}
