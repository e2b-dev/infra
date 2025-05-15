package orchestrators

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
	syncInterval   = 10 * time.Second
	syncMaxRetries = 3

	OrchestratorStatusHealthy   OrchestratorStatus = "healthy"
	OrchestratorStatusDraining  OrchestratorStatus = "draining"
	OrchestratorStatusUnhealthy OrchestratorStatus = "unhealthy"
)

type Orchestrator struct {
	ServiceId string
	NodeId    string

	SourceVersion string
	SourceCommit  string

	Host    string
	Status  OrchestratorStatus
	Startup time.Time
	Roles []e2bgrpcorchestratorinfo.ServiceInfoRole

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

	connection e2bgrpc.ClientConnInterface
}

func NewOrchestrator(ctx context.Context, host string) (*Orchestrator, error) {
	client, err := newClient(host)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	ctx, ctxCancel := context.WithCancel(ctx)

	o := &Orchestrator{
		Host:   host,
		Client: client,

		ctx:       ctx,
		ctxCancel: ctxCancel,
	}

	err = o.syncRun()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize orchestrator, maybe its not ready yet: %w", err)
	}

	// initialize background sync to update orchestrator running sandboxes
	go func() { o.sync() }()

	return o, nil
}

func (o *Orchestrator) Kill() error {
	o.ctxCancel()
	return o.Client.close()
}

func (o *Orchestrator) sync() {
	// Run the first sync immediately
	o.syncRun()

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			zap.L().Info("context done", zap.String("orchestrator sync id", o.ServiceId))
		case <-ticker.C:
			o.syncRun()
		}
	}
}

func (o *Orchestrator) syncRun() error {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	ctx, cancel := context.WithTimeout(o.ctx, syncInterval)
	defer cancel()

	for i := 0; i < syncMaxRetries; i++ {
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

func (o *Orchestrator) Close() error {
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
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	return &OrchestratorGRPCClient{
		Sandbox:    e2bgrpcorchestrator.NewSandboxServiceClient(conn),
		Template:   e2bgrpctemplatemanager.NewTemplateServiceClient(conn),
		Info:       e2bgrpcorchestratorinfo.NewInfoServiceClient(conn),
		connection: conn,
	}, nil
}

func (a *OrchestratorGRPCClient) close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
