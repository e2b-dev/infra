package clusters

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type GRPCClient struct {
	Info     infogrpc.InfoServiceClient
	Sandbox  orchestratorgrpc.SandboxServiceClient
	Volumes  orchestratorgrpc.VolumeServiceClient
	Template templatemanagergrpc.TemplateServiceClient

	Connection *grpc.ClientConn

	observeTarget string
}

func NewGRPCClient(conn *grpc.ClientConn, observeTarget string) *GRPCClient {
	return &GRPCClient{
		Connection:    conn,
		Info:          infogrpc.NewInfoServiceClient(conn),
		Sandbox:       orchestratorgrpc.NewSandboxServiceClient(conn),
		Volumes:       orchestratorgrpc.NewVolumeServiceClient(conn),
		Template:      templatemanagergrpc.NewTemplateServiceClient(conn),
		observeTarget: observeTarget,
	}
}

func (a *GRPCClient) Init(ctx context.Context) {
	e2bgrpc.ObserveConnection(ctx, a.Connection, a.observeTarget)
}

func (a *GRPCClient) Close() error {
	err := a.Connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close Connection: %w", err)
	}

	return nil
}
