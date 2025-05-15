package orchestrators

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	e2bgrpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	e2bgrpctemplatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type OrchestratorStatus string

const (
	orchestratorSyncInterval = 10 * time.Second

	OrchestratorStatusHealthy   OrchestratorStatus = "healthy"
	OrchestratorStatusDraining  OrchestratorStatus = "draining"
	OrchestratorStatusUnhealthy OrchestratorStatus = "unhealthy"
)

type Orchestrator struct {
	Id      string
	Version string

	Host   string
	Status OrchestratorStatus

	CanBuildTemplates bool
	CanSpawnSandboxes bool

	Client *OrchestratorGRPCClient

	VCpuUsed       atomic.Int64
	MemoryUsedInMB atomic.Int64
	DiskUsedInMB   atomic.Int64

	SandboxesRunning atomic.Int64

	mutex sync.Mutex

	ctx       context.Context
	ctxCancel context.CancelFunc
}

type OrchestratorGRPCClient struct {
	Sandbox  e2bgrpcorchestrator.SandboxServiceClient
	Template e2bgrpctemplatemanager.TemplateServiceClient
	Health   grpc_health_v1.HealthClient

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

		CanBuildTemplates: true,
		CanSpawnSandboxes: true,

		ctx:       ctx,
		ctxCancel: ctxCancel,
	}

	// initialize background sync to update orchestrator running sandboxes
	go func() { o.sync() }()

	return o, nil
}

func (o *Orchestrator) sync() {
	// Run the first sync immediately
	o.syncRun()

	ticker := time.NewTicker(orchestratorSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			zap.L().Info("context done", zap.String("orchestrator sync id", o.Id))
		case <-ticker.C:
			o.syncRun()
		}
	}
}

func (o *Orchestrator) syncRun() {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	ctx, cancel := context.WithTimeout(o.ctx, orchestratorSyncInterval)
	defer cancel()

	syncMaxRetries := 3
	syncSuccess := false

	for i := 0; i < syncMaxRetries; i++ {
		health, err := o.Client.Health.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		if err != nil {
			zap.L().Error("failed to check health", zap.String("orchestrator id", o.Id), zap.Error(err))
			continue
		}

		status := OrchestratorStatusHealthy

		switch health.Status {
		case grpc_health_v1.HealthCheckResponse_SERVING:
			status = OrchestratorStatusHealthy
		case grpc_health_v1.HealthCheckResponse_NOT_SERVING:
			status = OrchestratorStatusDraining
		case grpc_health_v1.HealthCheckResponse_UNKNOWN:
		case grpc_health_v1.HealthCheckResponse_SERVICE_UNKNOWN:
		}

		//if health.Status != grpc_health_v1.HealthCheckResponse {
	}

	// todo
	// todo
	// todo
	// todo
	println(syncSuccess)
	// ------
	// ------
	// ------
	// ------
	// ------
	// ------
	// ------
	// ------
	// ------
	// ------

	//o.Client.
	//	sandboxes, err := o.Client.Sandbox.List(ctx, &empty.Empty{})
	//if err != nil {
	//	zap.L().Error("failed to list sandboxes", zap.String("orchestrator id", o.Id), zap.Error(err))
	//	return
	//}

	cpuUsed := int64(0)
	diskUsed := int64(0)
	memoryUsed := int64(0)
	sandboxesRunning := int64(0)

	//// todo: we can stored actually all metadata about running sandboxes and use them for listing
	//for _, sandbox := range sandboxes.Sandboxes {
	//	sandboxesRunning++
	//
	//	diskUsed += sandbox.GetConfig().TotalDiskSizeMb
	//	memoryUsed += sandbox.GetConfig().RamMb
	//	cpuUsed += sandbox.GetConfig().Vcpu
	//}

	// metrics
	o.SandboxesRunning.Store(sandboxesRunning)
	o.MemoryUsedInMB.Store(memoryUsed)
	o.DiskUsedInMB.Store(diskUsed)
	o.VCpuUsed.Store(cpuUsed)

	// todo
	o.CanBuildTemplates = true
	o.CanSpawnSandboxes = true

	// todo
	o.Status = OrchestratorStatusHealthy
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

func newClient(host string) (*OrchestratorGRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), grpc.WithBlock(), grpc.WithTimeout(time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	return &OrchestratorGRPCClient{
		Sandbox:    e2bgrpcorchestrator.NewSandboxServiceClient(conn),
		Template:   e2bgrpctemplatemanager.NewTemplateServiceClient(conn),
		Health:     grpc_health_v1.NewHealthClient(conn),
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
