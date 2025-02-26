package template_manager

import (
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var (
	host = os.Getenv("TEMPLATE_MANAGER_ADDRESS")
)

type GRPCClient struct {
	Client     template_manager.TemplateServiceClient
	connection e2bgrpc.ClientConnInterface
}

func NewClient() (*GRPCClient, error) {
	keepaliveParam := grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                10 * time.Second, // Send ping every 10s
		Timeout:             2 * time.Second,  // Wait 2s for response
		PermitWithoutStream: true,
	})

	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), keepaliveParam)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := template_manager.NewTemplateServiceClient(conn)

	return &GRPCClient{Client: client, connection: conn}, nil
}

func (a *GRPCClient) Close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
