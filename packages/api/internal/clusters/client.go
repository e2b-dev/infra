package clusters

import (
	"fmt"

	"google.golang.org/grpc"

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
}

func NewGRPCClient(conn *grpc.ClientConn) *GRPCClient {
	return &GRPCClient{
		Connection: conn,
		Info:       infogrpc.NewInfoServiceClient(conn),
		Sandbox:    orchestratorgrpc.NewSandboxServiceClient(conn),
		Volumes:    orchestratorgrpc.NewVolumeServiceClient(conn),
		Template:   templatemanagergrpc.NewTemplateServiceClient(conn),
	}
}

func (a *GRPCClient) Close() error {
	err := a.Connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close Connection: %w", err)
	}

	return nil
}
