package grpc

import (
	"fmt"

	"google.golang.org/grpc"

	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type GRPCClient struct {
	Info     infogrpc.InfoServiceClient
	Sandbox  orchestratorgrpc.SandboxServiceClient
	Template tempaltemanagergrpc.TemplateServiceClient

	Connection *grpc.ClientConn
}

func (a *GRPCClient) Close() error {
	err := a.Connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
