package orchestrators

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type Orchestrator struct {
	Id      string
	Version string

	Host   string
	Port   int
	Status string

	CanBuild bool
	CanSpawn bool

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
	Sandbox orchestrator.SandboxServiceClient
	Health  grpc_health_v1.HealthClient

	connection e2bgrpc.ClientConnInterface
}

const (
	orchestratorSyncInterval = 10 * time.Second
)

func NewOrchestrator(ctx context.Context, id string, ip string, port int, version string, status string) (*Orchestrator, error) {
	host := fmt.Sprintf("%s:%d", ip, port)
	client, err := newClient(host)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC client: %w", err)
	}

	ctx, ctxCancel := context.WithCancel(ctx)

	o := &Orchestrator{
		Id:      id,
		Version: version,

		Host:   ip,
		Port:   port,
		Status: status,

		Client: client,

		CanBuild: true,
		CanSpawn: true,

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

	sandboxes, err := o.Client.Sandbox.List(o.ctx, &empty.Empty{})
	if err != nil {
		zap.L().Error("failed to list sandboxes", zap.String("orchestrator id", o.Id), zap.Error(err))
		return
	}

	cpuUsed := int64(0)
	diskUsed := int64(0)
	memoryUsed := int64(0)
	sandboxesRunning := int64(0)

	// todo: we can stored actually all metadata about running sandboxes and use them for listing
	for _, sandbox := range sandboxes.Sandboxes {
		sandboxesRunning++

		diskUsed += sandbox.GetConfig().TotalDiskSizeMb
		memoryUsed += sandbox.GetConfig().RamMb
		cpuUsed += sandbox.GetConfig().Vcpu
	}

	o.MemoryUsedInMB.Store(memoryUsed)
	o.DiskUsedInMB.Store(diskUsed)
	o.VCpuUsed.Store(cpuUsed)

	o.SandboxesRunning.Store(sandboxesRunning)
}

func (o *Orchestrator) Close() error {
	// close sync context
	o.ctxCancel()

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

	client := orchestrator.NewSandboxServiceClient(conn)
	health := grpc_health_v1.NewHealthClient(conn)

	return &OrchestratorGRPCClient{Sandbox: client, Health: health, connection: conn}, nil
}

func (a *OrchestratorGRPCClient) close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
